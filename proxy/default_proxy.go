package proxy

import (
	"context"
	"fmt"

	"github.com/nomand-zc/lumin-acpool/balancer"
	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-client/providers"
	"github.com/nomand-zc/lumin-client/queue"
	"github.com/nomand-zc/lumin-proxy/errs"
	"github.com/nomand-zc/lumin-proxy/plugin"
	"github.com/nomand-zc/lumin-proxy/protocol"
)

// DefaultProxy 是代理核心层的默认实现。
// 执行 BeforeRequest → Pick → Invoke → Report → AfterResponse 流程。
type DefaultProxy struct {
	balancer         balancer.Balancer
	providerRegistry ProviderRegistry
	hookRunner       plugin.HookRunner
}

// DefaultProxyOption 是 DefaultProxy 的配置选项。
type DefaultProxyOption func(*DefaultProxy)

// WithBalancer 设置 Balancer。
func WithBalancer(b balancer.Balancer) DefaultProxyOption {
	return func(p *DefaultProxy) {
		p.balancer = b
	}
}

// WithProviderRegistry 设置 ProviderRegistry。
func WithProviderRegistry(r ProviderRegistry) DefaultProxyOption {
	return func(p *DefaultProxy) {
		p.providerRegistry = r
	}
}

// WithHookRunner 设置 HookRunner。
func WithHookRunner(hr plugin.HookRunner) DefaultProxyOption {
	return func(p *DefaultProxy) {
		p.hookRunner = hr
	}
}

// NewDefaultProxy 创建默认代理实例。
func NewDefaultProxy(opts ...DefaultProxyOption) *DefaultProxy {
	p := &DefaultProxy{
		providerRegistry: func (providerType, providerName string)(providers.Provider, error) {
			provider := providers.GetProvider(providerType, providerName)
			if provider == nil {
				return nil, fmt.Errorf("provider %s/%s not found", providerType, providerName)
			}
			return provider, nil
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Handle 处理非流式代理请求。
func (p *DefaultProxy) Handle(ctx context.Context, req *protocol.Request) (*Result, error) {
	ctx, reqInfo, pickResult, provider, err := p.prepare(ctx, req)
	if err != nil {
		return nil, err
	}

	// ④ 调用上游
	req.ProviderRequest.Credential = pickResult.Account.Credential
	resp, err := provider.GenerateContent(ctx, req.ProviderRequest)

	// ⑤ Report
	if err != nil {
		_ = p.balancer.ReportFailure(ctx, pickResult.Account.ID, err)
		return nil, errs.Wrap(errs.CodeUpstreamError, "上游调用失败", err)
	}
	if resp.Error != nil {
		_ = p.balancer.ReportFailure(ctx, pickResult.Account.ID, fmt.Errorf("%s", resp.Error.Message))
	} else {
		_ = p.balancer.ReportSuccess(ctx, pickResult.Account.ID)
	}

	result := &Result{
		Response:     resp,
		AccountID:    pickResult.Account.ID,
		ProviderType: pickResult.ProviderKey.Type,
		ProviderName: pickResult.ProviderKey.Name,
	}

	// ⑥ AfterResponse 钩子
	if p.hookRunner != nil {
		respInfo := buildResponseInfo(result, resp)
		p.hookRunner.RunAfterResponse(ctx, reqInfo, respInfo, nil)
	}

	return result, nil
}

// HandleStream 处理流式代理请求。
func (p *DefaultProxy) HandleStream(ctx context.Context, req *protocol.Request) (*StreamResult, error) {
	ctx, reqInfo, pickResult, provider, err := p.prepare(ctx, req)
	if err != nil {
		return nil, err
	}

	// ④ 调用上游（流式）
	req.ProviderRequest.Credential = pickResult.Account.Credential
	stream, err := provider.GenerateContentStream(ctx, req.ProviderRequest)
	if err != nil {
		_ = p.balancer.ReportFailure(ctx, pickResult.Account.ID, err)
		return nil, errs.Wrap(errs.CodeUpstreamError, "上游流式调用失败", err)
	}

	// 包装流：注入钩子 + 自动 Report
	wrappedStream := newHookedStream(ctx, stream, p.hookRunner, p.balancer, reqInfo, pickResult)

	return &StreamResult{
		Stream:       wrappedStream,
		AccountID:    pickResult.Account.ID,
		ProviderType: pickResult.ProviderKey.Type,
		ProviderName: pickResult.ProviderKey.Name,
	}, nil
}

// prepare 执行请求处理的公共前置步骤：BeforeRequest 钩子 → 依赖检查 → Pick 账号 → 获取 Provider。
func (p *DefaultProxy) prepare(ctx context.Context, req *protocol.Request) (
	context.Context, *plugin.RequestInfo, *balancer.PickResult, providers.Provider, error,
) {
	// ① BeforeRequest 钩子
	reqInfo := buildRequestInfo(req)
	if p.hookRunner != nil {
		var err error
		ctx, err = p.hookRunner.RunBeforeRequest(ctx, reqInfo)
		if err != nil {
			return ctx, nil, nil, nil, err
		}
	}

	// 检查依赖是否已配置
	if p.balancer == nil {
		return ctx, nil, nil, nil, errs.New(errs.CodeInternal, "Balancer 未配置")
	}
	if p.providerRegistry == nil {
		return ctx, nil, nil, nil, errs.New(errs.CodeInternal, "ProviderRegistry 未配置")
	}

	// ② Pick 账号
	pickResult, err := p.balancer.Pick(ctx, &balancer.PickRequest{
		Model:          req.Model,
		UserID:         reqInfo.UserID,
		EnableFailover: true,
		MaxRetries:     2,
	})
	if err != nil {
		return ctx, nil, nil, nil, errs.Wrap(errs.CodeNoAvailableAccount, "无可用账号", err)
	}

	// ③ 获取 Provider 实例
	provider, err := p.providerRegistry(pickResult.ProviderKey.Type, pickResult.ProviderKey.Name)
	if err != nil {
		return ctx, nil, nil, nil, errs.Wrap(errs.CodeInternal, "获取 Provider 失败", err)
	}

	return ctx, reqInfo, pickResult, provider, nil
}

// hookedStream 包装原始流，注入 OnStreamChunk 钩子和 Report 逻辑。
type hookedStream struct {
	ctx        context.Context
	inner      queue.Consumer[*providers.Response]
	hookRunner plugin.HookRunner
	balancer   balancer.Balancer
	reqInfo    *plugin.RequestInfo
	pickResult *balancer.PickResult
	lastResp   *providers.Response
	reported   bool
}

func newHookedStream(
	ctx context.Context,
	inner queue.Consumer[*providers.Response],
	hookRunner plugin.HookRunner,
	balancer balancer.Balancer,
	reqInfo *plugin.RequestInfo,
	pickResult *balancer.PickResult,
) *hookedStream {
	return &hookedStream{
		ctx:        ctx,
		inner:      inner,
		hookRunner: hookRunner,
		balancer:   balancer,
		reqInfo:    reqInfo,
		pickResult: pickResult,
	}
}

func (s *hookedStream) Closed() bool {
	return s.inner.Closed()
}

func (s *hookedStream) Pop(ctx context.Context) (*providers.Response, error) {
	resp, err := s.inner.Pop(ctx)
	if err != nil {
		s.doReport(err)
		return nil, err
	}

	s.lastResp = resp
	s.processChunk(resp)

	// 最终 chunk 时 Report
	if resp.Done {
		s.doReport(nil)
	}

	return resp, nil
}

func (s *hookedStream) Each(ctx context.Context, fn func(*providers.Response) error) error {
	return s.inner.Each(ctx, func(resp *providers.Response) error {
		s.lastResp = resp
		s.processChunk(resp)

		if resp.Done {
			s.doReport(nil)
		}

		return fn(resp)
	})
}

func (s *hookedStream) Len() int {
	return s.inner.Len()
}

// processChunk 执行 OnStreamChunk 钩子。
func (s *hookedStream) processChunk(resp *providers.Response) {
	if s.hookRunner == nil {
		return
	}
	chunkInfo := &plugin.ResponseInfo{
		AccountID:    s.pickResult.Account.ID,
		ProviderType: s.pickResult.ProviderKey.Type,
		ProviderName: s.pickResult.ProviderKey.Name,
	}
	if resp.Usage != nil {
		chunkInfo.PromptTokens = resp.Usage.PromptTokens
		chunkInfo.CompletionTokens = resp.Usage.CompletionTokens
		chunkInfo.TotalTokens = resp.Usage.TotalTokens
	}
	s.hookRunner.RunOnStreamChunk(s.ctx, s.reqInfo, chunkInfo)
}

func (s *hookedStream) doReport(err error) {
	if s.reported {
		return
	}
	s.reported = true

	if err != nil {
		_ = s.balancer.ReportFailure(s.ctx, s.pickResult.Account.ID, err)
	} else {
		_ = s.balancer.ReportSuccess(s.ctx, s.pickResult.Account.ID)
	}

	// AfterResponse 钩子
	if s.hookRunner != nil {
		respInfo := &plugin.ResponseInfo{
			AccountID:    s.pickResult.Account.ID,
			ProviderType: s.pickResult.ProviderKey.Type,
			ProviderName: s.pickResult.ProviderKey.Name,
		}
		if s.lastResp != nil && s.lastResp.Usage != nil {
			respInfo.PromptTokens = s.lastResp.Usage.PromptTokens
			respInfo.CompletionTokens = s.lastResp.Usage.CompletionTokens
			respInfo.TotalTokens = s.lastResp.Usage.TotalTokens
		}
		s.hookRunner.RunAfterResponse(s.ctx, s.reqInfo, respInfo, err)
	}

	log.Debugf("流式请求完成: account_id=%s, provider=%s",
		s.pickResult.Account.ID,
		s.pickResult.ProviderKey.String(),
	)
}

// --- 辅助函数 ---

func buildRequestInfo(req *protocol.Request) *plugin.RequestInfo {
	info := &plugin.RequestInfo{
		Model:    req.Model,
		Stream:   req.Stream,
		Metadata: req.Metadata,
	}
	if req.Metadata != nil {
		if uid, ok := req.Metadata["user_id"].(string); ok {
			info.UserID = uid
		}
		if key, ok := req.Metadata["api_key"].(string); ok {
			info.APIKey = key
		}
	}
	return info
}

func buildResponseInfo(result *Result, resp *providers.Response) *plugin.ResponseInfo {
	info := &plugin.ResponseInfo{
		AccountID:    result.AccountID,
		ProviderType: result.ProviderType,
		ProviderName: result.ProviderName,
	}
	if resp != nil {
		info.Model = resp.Model
		if resp.Usage != nil {
			info.PromptTokens = resp.Usage.PromptTokens
			info.CompletionTokens = resp.Usage.CompletionTokens
			info.TotalTokens = resp.Usage.TotalTokens
		}
	}
	return info
}
