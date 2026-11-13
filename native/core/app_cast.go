package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type castDevice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	control string
	service string
}

type nativeCast struct {
	mu      sync.Mutex
	devices map[string]castDevice
	device  castDevice
	token   string
	active  bool
}

type castDescriptionDevice struct {
	Name     string `xml:"friendlyName"`
	Services []struct {
		Type    string `xml:"serviceType"`
		Control string `xml:"controlURL"`
	} `xml:"serviceList>service"`
	Devices []castDescriptionDevice `xml:"deviceList>device"`
}

type castDescription struct {
	URLBase string                `xml:"URLBase"`
	Device  castDescriptionDevice `xml:"device"`
}

var castClient = &http.Client{Timeout: 6 * time.Second, Transport: &http.Transport{}}

const castSearch = "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: urn:schemas-upnp-org:service:AVTransport:1\r\n\r\n"

func castSearchLocations(ctx context.Context) []string {
	bind := []net.IP{nil}
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok {
				if ip := network.IP.To4(); ip != nil && !ip.IsLoopback() {
					bind = append(bind, ip)
				}
			}
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	target := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	var mu sync.Mutex
	var group sync.WaitGroup
	found := map[string]bool{}
	for _, ip := range bind {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
		if err != nil {
			continue
		}
		group.Add(1)
		go func() {
			defer group.Done()
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
			defer stop()
			_ = conn.SetReadDeadline(deadline)
			for range 2 {
				_, _ = conn.WriteToUDP([]byte(castSearch), target)
			}
			buffer := make([]byte, 4096)
			for {
				size, _, err := conn.ReadFromUDP(buffer)
				if err != nil {
					return
				}
				response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buffer[:size])), nil)
				if err != nil {
					continue
				}
				location := response.Header.Get("Location")
				_ = response.Body.Close()
				if parsed, err := url.Parse(location); err == nil && parsed.Scheme == "http" && parsed.Host != "" {
					mu.Lock()
					found[location] = true
					mu.Unlock()
				}
			}
		}()
	}
	group.Wait()
	locations := make([]string, 0, len(found))
	for location := range found {
		locations = append(locations, location)
	}
	return locations
}

func castTransportService(device castDescriptionDevice) (string, string, bool) {
	for _, service := range device.Services {
		if strings.Contains(service.Type, ":service:AVTransport:") {
			return strings.TrimSpace(service.Type), strings.TrimSpace(service.Control), true
		}
	}
	for _, child := range device.Devices {
		if service, control, found := castTransportService(child); found {
			return service, control, true
		}
	}
	return "", "", false
}

func castDescribe(ctx context.Context, location string) (castDevice, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return castDevice{}, err
	}
	response, err := castClient.Do(request)
	if err != nil {
		return castDevice{}, err
	}
	defer response.Body.Close()
	var description castDescription
	if err := xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&description); err != nil {
		return castDevice{}, err
	}
	service, control, found := castTransportService(description.Device)
	if !found {
		return castDevice{}, errors.New("设备不支持视频投屏")
	}
	base, err := url.Parse(location)
	if err != nil {
		return castDevice{}, err
	}
	if declared, err := url.Parse(strings.TrimSpace(description.URLBase)); err == nil && declared.Host != "" {
		base = declared
	}
	reference, err := url.Parse(control)
	if err != nil {
		return castDevice{}, err
	}
	name := strings.TrimSpace(description.Device.Name)
	if name == "" {
		name = base.Hostname()
	}
	return castDevice{ID: location, Name: name, control: base.ResolveReference(reference).String(), service: service}, nil
}

func (engine *nativeEngine) castDiscover(ctx context.Context) []castDevice {
	locations := castSearchLocations(ctx)
	devices := make([]castDevice, len(locations))
	var group sync.WaitGroup
	for index, location := range locations {
		group.Add(1)
		go func() {
			defer group.Done()
			devices[index], _ = castDescribe(ctx, location)
		}()
	}
	group.Wait()
	known := map[string]castDevice{}
	items := []castDevice{}
	for _, device := range devices {
		if device.ID != "" {
			known[device.ID] = device
			items = append(items, device)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	engine.cast.mu.Lock()
	if engine.cast.active {
		known[engine.cast.device.ID] = engine.cast.device
	}
	engine.cast.devices = known
	engine.cast.mu.Unlock()
	return items
}

func castEscape(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}

func castInvoke(ctx context.Context, device castDevice, action string, arguments ...string) (map[string]string, error) {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	fmt.Fprintf(&body, `<u:%s xmlns:u="%s"><InstanceID>0</InstanceID>`, action, castEscape(device.service))
	for index := 0; index+1 < len(arguments); index += 2 {
		fmt.Fprintf(&body, "<%s>%s</%s>", arguments[index], castEscape(arguments[index+1]), arguments[index])
	}
	fmt.Fprintf(&body, `</u:%s></s:Body></s:Envelope>`, action)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, device.control, strings.NewReader(body.String()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	request.Header["SOAPACTION"] = []string{`"` + device.service + "#" + action + `"`}
	response, err := castClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("连接不上投屏设备「%s」：%w", device.Name, err)
	}
	defer response.Body.Close()
	values := map[string]string{}
	decoder := xml.NewDecoder(io.LimitReader(response.Body, 1<<20))
	name := ""
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch element := token.(type) {
		case xml.StartElement:
			name = element.Name.Local
		case xml.CharData:
			if text := strings.TrimSpace(string(element)); text != "" && name != "" {
				values[name] += text
			}
		case xml.EndElement:
			name = ""
		}
	}
	if response.StatusCode != http.StatusOK {
		detail := "HTTP " + strconv.Itoa(response.StatusCode)
		if code := values["errorCode"]; code != "" {
			detail = "错误码 " + code + " " + values["errorDescription"]
		}
		return nil, fmt.Errorf("投屏设备「%s」拒绝了 %s 操作（%s）", device.Name, action, strings.TrimSpace(detail))
	}
	return values, nil
}

func castClock(milliseconds int64) string {
	seconds := max(0, milliseconds) / 1000
	return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

func castMilliseconds(clock string) int64 {
	parts := strings.Split(strings.TrimSpace(clock), ":")
	if len(parts) != 3 {
		return 0
	}
	hours, hourErr := strconv.Atoi(parts[0])
	minutes, minuteErr := strconv.Atoi(parts[1])
	seconds, secondErr := strconv.ParseFloat(parts[2], 64)
	if hourErr != nil || minuteErr != nil || secondErr != nil {
		return 0
	}
	return int64(hours)*3600000 + int64(minutes)*60000 + int64(seconds*1000)
}

func castState(transport string) string {
	switch transport {
	case "PLAYING":
		return "playing"
	case "PAUSED_PLAYBACK":
		return "paused"
	case "TRANSITIONING":
		return "loading"
	default:
		return "stopped"
	}
}

func (engine *nativeEngine) castStatus(ctx context.Context, device castDevice) (map[string]any, error) {
	transport, err := castInvoke(ctx, device, "GetTransportInfo")
	if err != nil {
		return nil, err
	}
	position, err := castInvoke(ctx, device, "GetPositionInfo")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"state":      castState(transport["CurrentTransportState"]),
		"positionMs": castMilliseconds(position["RelTime"]),
		"durationMs": castMilliseconds(position["TrackDuration"]),
	}, nil
}

func castMetadata(title, address, contentType string) string {
	return `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item id="0" parentID="-1" restricted="1"><dc:title>` +
		castEscape(title) + `</dc:title><upnp:class>object.item.videoItem</upnp:class><res protocolInfo="http-get:*:` + contentType + `:*">` +
		castEscape(address) + `</res></item></DIDL-Lite>`
}

func (engine *nativeEngine) castPlay(ctx context.Context, input nativeInput) (any, error) {
	engine.cast.mu.Lock()
	device, known := engine.cast.devices[input.Device]
	previous, previousToken, wasActive := engine.cast.device, engine.cast.token, engine.cast.active
	engine.cast.mu.Unlock()
	if !known {
		return nil, errors.New("投屏设备已失效，请重新搜索")
	}
	if _, _, valid := SplitProviderDramaID(input.Drama.ID); !valid {
		return nil, errors.New("剧集信息无效")
	}
	task := Task{DramaID: input.Drama.ID, DramaTitle: input.Drama.Title, Chapter: input.Chapter, Index: input.Index}
	resolved, err := engine.downloader.resolveProviderMedia(ctx, task)
	if err != nil {
		return nil, err
	}
	var media *ProviderMedia
	for _, option := range nativePlaybackChoices(resolved, input.Quality).media {
		if IsProviderHTTPMediaURL(option.URL) {
			media = &option
			break
		}
	}
	if media == nil {
		return nil, errors.New("站源未返回有效的播放地址")
	}
	stream, err := engine.streamServer()
	if err != nil {
		return nil, err
	}
	control, err := url.Parse(device.control)
	if err != nil {
		return nil, err
	}
	origin, err := stream.lanOrigin(control.Host)
	if err != nil {
		return nil, err
	}
	local, token := stream.nativeOpen(*media)
	if len(media.CENCKey) > 0 {
		if err := stream.nativeDecrypt(ctx, token, *media); err != nil {
			stream.nativeRelease(token)
			return nil, err
		}
	}
	address := origin + strings.TrimPrefix(local, stream.address)
	contentType := "video/mp4"
	if strings.HasSuffix(local, ".m3u8") {
		contentType = "application/vnd.apple.mpegurl"
	}
	if wasActive {
		_, _ = castInvoke(ctx, previous, "Stop")
		stream.nativeRelease(previousToken)
	}
	engine.cast.mu.Lock()
	engine.cast.device, engine.cast.token, engine.cast.active = device, token, true
	engine.cast.mu.Unlock()
	title := strings.TrimSpace(input.Drama.Title + " 第 " + strconv.Itoa(input.Index) + " 集")
	if _, err := castInvoke(ctx, device, "SetAVTransportURI", "CurrentURI", address, "CurrentURIMetaData", castMetadata(title, address, contentType)); err != nil {
		engine.castRelease()
		return nil, err
	}
	if _, err := castInvoke(ctx, device, "Play", "Speed", "1"); err != nil {
		engine.castRelease()
		return nil, err
	}
	if input.StartMS > 0 {
		engine.castResume(ctx, device, input.StartMS)
	}
	return map[string]string{"name": device.Name}, nil
}

func (engine *nativeEngine) castResume(ctx context.Context, device castDevice, startMS int64) {
	for range 20 {
		transport, err := castInvoke(ctx, device, "GetTransportInfo")
		if err != nil {
			return
		}
		if transport["CurrentTransportState"] == "PLAYING" {
			_, _ = castInvoke(ctx, device, "Seek", "Unit", "REL_TIME", "Target", castClock(startMS))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (engine *nativeEngine) castRelease() {
	engine.cast.mu.Lock()
	token := engine.cast.token
	engine.cast.token, engine.cast.active = "", false
	engine.cast.mu.Unlock()
	engine.mu.Lock()
	stream := engine.stream
	engine.mu.Unlock()
	if stream != nil {
		stream.nativeRelease(token)
		stream.lanClose()
	}
}

func (engine *nativeEngine) nativeCast(ctx context.Context, input nativeInput) (any, error) {
	if input.Command == "discover" {
		return map[string]any{"items": engine.castDiscover(ctx)}, nil
	}
	if input.Command == "play" {
		return engine.castPlay(ctx, input)
	}
	engine.cast.mu.Lock()
	device, active := engine.cast.device, engine.cast.active
	engine.cast.mu.Unlock()
	if input.Command == "active" {
		return map[string]bool{"active": active}, nil
	}
	if !active {
		if input.Command == "stop" {
			return true, nil
		}
		return nil, errors.New("当前没有正在进行的投屏")
	}
	switch input.Command {
	case "status":
		return engine.castStatus(ctx, device)
	case "pause":
		_, err := castInvoke(ctx, device, "Pause")
		return true, err
	case "resume":
		_, err := castInvoke(ctx, device, "Play", "Speed", "1")
		return true, err
	case "seek":
		_, err := castInvoke(ctx, device, "Seek", "Unit", "REL_TIME", "Target", castClock(input.StartMS))
		return true, err
	case "stop":
		_, err := castInvoke(ctx, device, "Stop")
		engine.castRelease()
		return true, err
	default:
		return nil, errors.New("不支持的投屏操作")
	}
}
