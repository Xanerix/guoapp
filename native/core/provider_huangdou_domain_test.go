package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type huangdouStubTransport struct {
	mu        sync.Mutex
	hosts     []string
	deadlines []time.Duration
	respond   func(req *http.Request) (*http.Response, error)
}

func (s *huangdouStubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	remaining := time.Duration(-1)
	if deadline, ok := req.Context().Deadline(); ok {
		remaining = time.Until(deadline)
	}
	s.mu.Lock()
	s.hosts = append(s.hosts, req.URL.Hostname())
	s.deadlines = append(s.deadlines, remaining)
	s.mu.Unlock()
	return s.respond(req)
}

func (s *huangdouStubTransport) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hosts...)
}

func (s *huangdouStubTransport) seenDeadlines() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.deadlines...)
}

func newHuangdouStubDownloader(cfg Config, respond func(*http.Request) (*http.Response, error)) (*Downloader, *huangdouStubTransport) {
	stub := &huangdouStubTransport{respond: respond}
	return &Downloader{
		cfg:           cfg,
		client:        &http.Client{Transport: stub},
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}, stub
}

func huangdouBusinessErrorResponse() *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":"n","errorCode":"813005","msg":"权限不足"}`)),
	}
}

func TestHuangdouHostPoolConsistency(t *testing.T) {
	if len(huangdouHostPool) < 2 {
		t.Fatalf("域名池至少要有一个备用域名: %v", huangdouHostPool)
	}
	if got := huangdouNextHostURL(huangdouBaseURL); got != "https://"+huangdouHostPool[1] {
		t.Fatalf("主域名不是轮转起点: %s -> %s", huangdouBaseURL, got)
	}
	seen := map[string]bool{}
	for _, pooled := range huangdouHostPool {
		if seen[pooled] {
			t.Fatalf("域名池有重复项: %s", pooled)
		}
		seen[pooled] = true
		if !isHuangdouHost(pooled) {
			t.Fatalf("池内域名未被识别: %s", pooled)
		}
		if got := canonicalProviderSource(pooled); got != sourceHuangdou {
			t.Fatalf("域名未映射回源标识: %s -> %s", pooled, got)
		}
		if !isHuangdouHost("https://" + pooled + "/api/drama/list") {
			t.Fatalf("完整 URL 未被识别: %s", pooled)
		}
		if got := providerSourceForURL("https://" + pooled + "/api/drama/list"); got != sourceHuangdou {
			t.Fatalf("providerSourceForURL 未识别池内域名: %s -> %q", pooled, got)
		}
		if host := huangdouHostname("HTTPS://" + strings.ToUpper(pooled) + ":443/x?y=1"); host != pooled {
			t.Fatalf("裸域名归一化错误: %s -> %s", pooled, host)
		}
		if !isHuangdouHost("https://user:pass@"+pooled+":443/x") || !isHuangdouHost("user:pass@"+pooled) {
			t.Fatalf("带 userinfo 的池内域名未被识别: %s", pooled)
		}
	}
	for _, rejected := range []string{
		"tideember.cc", "xqjurgek.top", "example.com", "",
		"ferncider.cc.evil.com", "evil.com/ferncider.cc", "ferncider.cc@evil.com",
		"https://ferncider.cc@evil.com/x",
	} {
		if isHuangdouHost(rejected) {
			t.Fatalf("非池内域名被误判: %q", rejected)
		}
	}
	for _, legacy := range []string{"tideember.cc", "xqjurgek.top"} {
		if got := canonicalProviderSource(legacy); got != sourceHuangdou {
			t.Fatalf("历史域名别名丢失: %s -> %q", legacy, got)
		}
	}
}

func TestHuangdouHostRotationOrder(t *testing.T) {
	walked := make([]string, 0, len(huangdouHostPool))
	current := huangdouBaseURL
	for range huangdouHostPool {
		walked = append(walked, huangdouHostname(current))
		current = huangdouNextHostURL(current)
	}
	if strings.Join(walked, ",") != strings.Join(huangdouHostPool[:], ",") {
		t.Fatalf("轮转顺序与池不一致: %v", walked)
	}
	if huangdouHostname(current) != huangdouHostPool[0] {
		t.Fatalf("轮转未回绕到池首: %s", current)
	}
	if got := huangdouNextHostURL("https://tideember.cc"); got != "https://"+huangdouHostPool[0] {
		t.Fatalf("未知域名未回落到池首: %s", got)
	}
}

func TestHuangdouCallWalksWholePool(t *testing.T) {
	d, stub := newHuangdouStubDownloader(Config{Retries: 2}, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp: DNS 解析失败")
	})
	client := newHuangdouAPIClient(d)
	var out any
	start := time.Now()
	err := client.call(context.Background(), "/drama/list", map[string]any{}, &out)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("全网络失败时不应返回成功")
	}
	hosts := stub.seen()
	if len(hosts) != len(huangdouHostPool) {
		t.Fatalf("应尝试全部 %d 个域名，实际 %d 个: %v", len(huangdouHostPool), len(hosts), hosts)
	}
	for index, pooled := range huangdouHostPool {
		if hosts[index] != pooled {
			t.Fatalf("第 %d 次尝试应访问 %s，实际 %s（序列 %v）", index+1, pooled, hosts[index], hosts)
		}
	}
	if huangdouHostname(client.host) != hosts[len(hosts)-1] {
		t.Fatalf("结束 host 与实际最后一次请求不一致: %s vs %v", client.host, hosts)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("已尝试 %d 个域名", len(huangdouHostPool))) {
		t.Fatalf("错误信息缺少尝试域名的数量: %v", err)
	}
	if !strings.Contains(err.Error(), "DNS 解析失败") {
		t.Fatalf("错误信息丢失了底层原因: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("轮转不应退避，整轮耗时 %v", elapsed)
	}
	if deadlines := stub.seenDeadlines(); len(deadlines) != len(hosts) {
		t.Fatalf("deadline 记录数不匹配: %d vs %d", len(deadlines), len(hosts))
	} else {
		if deadlines[0] < huangdouFirstAttemptTimeout-time.Second || deadlines[0] > huangdouFirstAttemptTimeout+time.Second {
			t.Fatalf("首跳预算应约 %v，实际 %v", huangdouFirstAttemptTimeout, deadlines[0])
		}
		for index := 1; index < len(deadlines); index++ {
			if deadlines[index] > huangdouAttemptTimeout+time.Second {
				t.Fatalf("第 %d 次轮转预算应不超过 %v，实际 %v", index+1, huangdouAttemptTimeout, deadlines[index])
			}
		}
	}
}

func TestHuangdouCallKeepsConfiguredHost(t *testing.T) {
	d, stub := newHuangdouStubDownloader(Config{Retries: 2, HuangdouURL: "https://ferncider.cc"}, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("boom")
	})
	client := newHuangdouAPIClient(d)
	var out any
	err := client.call(context.Background(), "/drama/list", map[string]any{}, &out)
	if err == nil {
		t.Fatal("失败时不应返回成功")
	}
	hosts := stub.seen()
	if len(hosts) != 2 || hosts[0] != "ferncider.cc" || hosts[1] != "ferncider.cc" {
		t.Fatalf("显式配置站点时不应轮转: %v", hosts)
	}
	if strings.Contains(err.Error(), "个域名") {
		t.Fatalf("单站点重试不应使用多域名文案: %v", err)
	}
}

func TestHuangdouCallStopsOnBusinessError(t *testing.T) {
	d, stub := newHuangdouStubDownloader(Config{Retries: 2}, func(*http.Request) (*http.Response, error) {
		return huangdouBusinessErrorResponse(), nil
	})
	client := newHuangdouAPIClient(d)
	var out any
	err := client.call(context.Background(), "/drama/play", map[string]any{}, &out)
	var apiErr *huangdouAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回业务错误: %v", err)
	}
	hosts := stub.seen()
	if len(hosts) != 1 {
		t.Fatalf("业务错误不应重试或轮转: %v", hosts)
	}
	if huangdouHostname(client.host) != hosts[0] {
		t.Fatalf("业务错误后不应切换域名: %s", client.host)
	}
}

func TestHuangdouCallKeepsBusinessErrorAfterRotate(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	d, stub := newHuangdouStubDownloader(Config{Retries: 2}, func(*http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			return nil, fmt.Errorf("dial tcp: DNS 解析失败")
		}
		return huangdouBusinessErrorResponse(), nil
	})
	client := newHuangdouAPIClient(d)
	var out any
	err := client.call(context.Background(), "/drama/play", map[string]any{}, &out)
	var apiErr *huangdouAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应保留业务错误类型: %v", err)
	}
	if len(stub.seen()) != 2 {
		t.Fatalf("应在第 2 个域名拿到业务错误后停止: %v", stub.seen())
	}
	if strings.Contains(err.Error(), "已尝试") {
		t.Fatalf("业务错误不应被包装成连接失败: %v", err)
	}
}

func TestHuangdouCallRespectsContextCancel(t *testing.T) {
	d, stub := newHuangdouStubDownloader(Config{Retries: 2}, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp: i/o timeout")
	})
	client := newHuangdouAPIClient(d)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out any
	err := client.call(ctx, "/drama/list", map[string]any{}, &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的 ctx 应原样返回取消错误: %v", err)
	}
	if hosts := stub.seen(); len(hosts) != 0 {
		t.Fatalf("ctx 已取消时不应发出请求: %v", hosts)
	}
}

func TestHuangdouAttemptTimeoutSplitsBudget(t *testing.T) {
	client := &huangdouAPIClient{d: &Downloader{cfg: defaultConfig()}}
	if got := client.attemptTimeout(context.Background(), 1, huangdouAttemptTimeout); got != huangdouAttemptTimeout {
		t.Fatalf("没有 deadline 时应回到给定上限: %v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	got := client.attemptTimeout(ctx, len(huangdouHostPool), huangdouAttemptTimeout)
	if got < 500*time.Millisecond || got > time.Second+50*time.Millisecond {
		t.Fatalf("6s 预算分给 %d 个域名应约 1s，实际 %v", len(huangdouHostPool), got)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel2()
	if got := client.attemptTimeout(ctx2, 2, huangdouAttemptTimeout); got != huangdouAttemptTimeout {
		t.Fatalf("预算充足时应使用给定上限: %v", got)
	}
	ctx3, cancel3 := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel3()
	if got := client.attemptTimeout(ctx3, 6, huangdouAttemptTimeout); got < time.Second {
		t.Fatalf("单次超时下限应为 1s，实际 %v", got)
	}
}

func TestHuangdouCallSharesTightBudget(t *testing.T) {
	d, stub := newHuangdouStubDownloader(Config{Retries: 2}, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp: DNS 解析失败")
	})
	client := newHuangdouAPIClient(d)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var out any
	if err := client.call(ctx, "/drama/list", map[string]any{}, &out); err == nil {
		t.Fatal("全网络失败时不应返回成功")
	}
	deadlines := stub.seenDeadlines()
	if len(deadlines) != len(huangdouHostPool) {
		t.Fatalf("应尝试全部 %d 个域名，实际 %d 个", len(huangdouHostPool), len(deadlines))
	}
	last := deadlines[len(deadlines)-1]
	if last < 5*time.Second {
		t.Fatalf("最后一个域名只拿到 %v，预算没有按剩余次数分配", last)
	}
	if last > huangdouAttemptTimeout+time.Second {
		t.Fatalf("最后一个域名拿到 %v，超过单次上限 %v", last, huangdouAttemptTimeout)
	}
}

func TestHuangdouCoverRefererFollowsPool(t *testing.T) {
	d := &Downloader{cfg: defaultConfig(), providerHosts: map[string]string{}}
	for _, pooled := range huangdouHostPool {
		cover := "https://" + pooled + "/covers/a.jpg"
		want := "https://" + pooled + "/home"
		if got := nativeCoverReferer(d, sourceHuangdou, cover); got != want {
			t.Fatalf("封面 referer 未跟随域名池: %s -> %s (want %s)", pooled, got, want)
		}
	}
	d.providerHosts[sourceHuangdou] = "https://" + huangdouHostPool[1]
	if got := nativeCoverReferer(d, sourceHuangdou, "https://example.com/covers/a.jpg"); got != "https://"+huangdouHostPool[1]+"/home" {
		t.Fatalf("非池域名封面未回落到首选站点: %s", got)
	}
}
