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

// 黄豆站点会不定期换域名，这里保证域名池、轮转顺序、源标识反查三者保持一致。

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
		cfg: cfg,
		// 不设 Client.Timeout：否则它会和请求 ctx 的 deadline 取更小值，把预算断言搅乱
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
		// 带账号信息的地址归一化后仍是池内域名；账号部分不参与匹配
		if !isHuangdouHost("https://user:pass@"+pooled+":443/x") || !isHuangdouHost("user:pass@"+pooled) {
			t.Fatalf("带 userinfo 的池内域名未被识别: %s", pooled)
		}
	}
	// 不得把非池内域名误判为黄豆：这里既有已失效的旧域名，也有形似域名
	for _, rejected := range []string{
		"tideember.cc", "xqjurgek.top", "example.com", "",
		"ferncider.cc.evil.com", "evil.com/ferncider.cc", "ferncider.cc@evil.com",
		"https://ferncider.cc@evil.com/x",
	} {
		if isHuangdouHost(rejected) {
			t.Fatalf("非池内域名被误判: %q", rejected)
		}
	}
	// 已失效的旧域名不再进池，但作为历史 source 别名仍要能识别回黄豆
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
	// 不在池中的 host（例如历史遗留域名）应当回落到池首
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
	// 期望值必须来自域名池本身，而不是被测的实现常量，否则池变长了测试也发现不了
	hosts := stub.seen()
	if len(hosts) != len(huangdouHostPool) {
		t.Fatalf("应尝试全部 %d 个域名，实际 %d 个: %v", len(huangdouHostPool), len(hosts), hosts)
	}
	for index, pooled := range huangdouHostPool {
		if hosts[index] != pooled {
			t.Fatalf("第 %d 次尝试应访问 %s，实际 %s（序列 %v）", index+1, pooled, hosts[index], hosts)
		}
	}
	// 结束后 host 应停在最后实际请求过的域名上
	if huangdouHostname(client.host) != hosts[len(hosts)-1] {
		t.Fatalf("结束 host 与实际最后一次请求不一致: %s vs %v", client.host, hosts)
	}
	// 全池失败要说明试过几个域名，否则用户只看到最后一个陌生域名的报错，容易误判
	if !strings.Contains(err.Error(), fmt.Sprintf("已尝试 %d 个域名", len(huangdouHostPool))) {
		t.Fatalf("错误信息缺少尝试域名的数量: %v", err)
	}
	if !strings.Contains(err.Error(), "DNS 解析失败") {
		t.Fatalf("错误信息丢失了底层原因: %v", err)
	}
	// 换域名是换目标，不该退避；一轮快速失败应当毫秒级完成。
	// 一旦有人把轮转改回逐次退避，这里会因为 1+2+3+4+5 秒而超时。
	if elapsed > 2*time.Second {
		t.Fatalf("轮转不应退避，整轮耗时 %v", elapsed)
	}
	// 首跳预算应比后续轮转宽裕，避免站点只是慢就被迫轮转
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
	// 同一域名重试不是「试了多个域名」，不能套上多域名的文案
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

// 第一个域名网络失败后，第二个域名给出了业务拒绝：这已经是站点明确答复，
// 必须原样返回业务错误，不能被包装成「已尝试 N 个域名连接失败」。
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

// 域名可解析但连不上时，每个域名都要分到预算，不能被排在前面的一两个吃光
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
	// 剩余预算足够时不应被均分逻辑压低到上限以下
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel2()
	if got := client.attemptTimeout(ctx2, 2, huangdouAttemptTimeout); got != huangdouAttemptTimeout {
		t.Fatalf("预算充足时应使用给定上限: %v", got)
	}
	// 极端紧的预算仍要保证一个可用的下限，不能算出 0 让请求立即失败
	ctx3, cancel3 := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel3()
	if got := client.attemptTimeout(ctx3, 6, huangdouAttemptTimeout); got < time.Second {
		t.Fatalf("单次超时下限应为 1s，实际 %v", got)
	}
}

// 外层预算紧绷时，剩余时间要按「剩余次数」均分：越往后每个域名分到的越多，
// 但都被单次上限压住。这条同时锁住 call → attemptTimeout → callOnce 的传参链路。
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
	// 失败是毫秒级的，预算几乎不减少，所以最后一次应分到接近单次上限；
	// 若均分用了固定的总次数（而不是剩余次数），最后一次只会拿到 12/6=2s。
	last := deadlines[len(deadlines)-1]
	if last < 5*time.Second {
		t.Fatalf("最后一个域名只拿到 %v，预算没有按剩余次数分配", last)
	}
	// 同时确认单次上限真的生效：若 callOnce 忽略了传入的超时（例如写死 20s），这里会超标。
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
	// 非池域名的封面必须回落到首选站点，不能误判成池内域名
	d.providerHosts[sourceHuangdou] = "https://" + huangdouHostPool[1]
	if got := nativeCoverReferer(d, sourceHuangdou, "https://example.com/covers/a.jpg"); got != "https://"+huangdouHostPool[1]+"/home" {
		t.Fatalf("非池域名封面未回落到首选站点: %s", got)
	}
}
