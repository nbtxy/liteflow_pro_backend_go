package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/config"
)

type OAuthService struct {
	cfg            config.MCPConfig
	callbackURL    string
	states         sync.Map // state -> *oauthStateEntry
	pkceStore      sync.Map // state -> codeVerifier
	dynamicClients sync.Map // provider -> *dynamicClientInfo
}

type oauthStateEntry struct {
	UserID    uuid.UUID
	Provider  string
	ServerURL string
	CreatedAt time.Time
}

func (e *oauthStateEntry) isExpired() bool {
	return time.Since(e.CreatedAt) > 10*time.Minute
}

type providerConfig struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
	RegisterURL  string // MCP OAuth 2.1 dynamic registration endpoint
	IsDynamic    bool
}

type dynamicClientInfo struct {
	ClientID     string
	ClientSecret string
	RegisteredAt time.Time
}

type OAuthTokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
}

func NewOAuthService(cfg config.MCPConfig) *OAuthService {
	callbackURL := cfg.OAuthCallbackURL
	if callbackURL == "" {
		callbackURL = "http://localhost:3000/chat/connectors/oauth-callback"
	}
	return &OAuthService{cfg: cfg, callbackURL: callbackURL}
}

func (s *OAuthService) getProvider(provider string) (*providerConfig, bool) {
	switch provider {
	case "github":
		if s.cfg.GitHubClientID == "" {
			return nil, false
		}
		return &providerConfig{
			AuthorizeURL: "https://github.com/login/oauth/authorize",
			TokenURL:     "https://github.com/login/oauth/access_token",
			ClientID:     s.cfg.GitHubClientID,
			ClientSecret: s.cfg.GitHubClientSecret,
			Scope:        s.cfg.GitHubScope,
		}, true
	case "supabase":
		if s.cfg.SupabaseClientID == "" {
			return nil, false
		}
		return &providerConfig{
			AuthorizeURL: "https://api.supabase.com/v1/oauth/authorize",
			TokenURL:     "https://api.supabase.com/v1/oauth/token",
			ClientID:     s.cfg.SupabaseClientID,
			ClientSecret: s.cfg.SupabaseClientSecret,
			Scope:        s.cfg.SupabaseScope,
		}, true
	case "notion":
		return &providerConfig{
			AuthorizeURL: "https://mcp.notion.com/authorize",
			TokenURL:     "https://mcp.notion.com/token",
			RegisterURL:  "https://mcp.notion.com/register",
			IsDynamic:    true,
		}, true
	case "cloudflare":
		return &providerConfig{
			AuthorizeURL: "https://mcp.cloudflare.com/authorize",
			TokenURL:     "https://mcp.cloudflare.com/token",
			RegisterURL:  "https://mcp.cloudflare.com/register",
			IsDynamic:    true,
		}, true
	default:
		return nil, false
	}
}

func (s *OAuthService) IsProviderRegistered(provider string) bool {
	_, ok := s.getProvider(provider)
	return ok
}

func (s *OAuthService) GenerateAuthURL(userID uuid.UUID, provider, serverURL string) (authURL, state string, err error) {
	pcfg, ok := s.getProvider(provider)
	if !ok {
		return "", "", fmt.Errorf("该服务暂不支持 OAuth 授权，请使用 Token 方式连接")
	}

	state = uuid.NewString()

	s.states.Store(state, &oauthStateEntry{
		UserID:    userID,
		Provider:  provider,
		ServerURL: serverURL,
		CreatedAt: time.Now(),
	})

	// PKCE
	codeVerifier := generateCodeVerifier()
	codeChallenge := generateCodeChallenge(codeVerifier)
	s.pkceStore.Store(state, codeVerifier)

	clientID := pcfg.ClientID

	// Dynamic client registration for MCP OAuth 2.1 providers
	if pcfg.IsDynamic {
		dci, err := s.ensureDynamicClient(provider, pcfg)
		if err != nil {
			return "", "", fmt.Errorf("动态客户端注册失败: %w", err)
		}
		clientID = dci.ClientID
	}

	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", s.callbackURL)
	params.Set("state", state)
	params.Set("response_type", "code")
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	if pcfg.Scope != "" {
		params.Set("scope", pcfg.Scope)
	}

	authURL = pcfg.AuthorizeURL + "?" + params.Encode()
	slog.Info("OAuth authorize generated", "provider", provider, "userId", userID)
	return authURL, state, nil
}

func (s *OAuthService) ExchangeCode(ctx context.Context, provider, code string) (OAuthTokenSet, error) {
	return s.ExchangeCodeWithState(ctx, provider, code, "")
}

func (s *OAuthService) ExchangeCodeWithState(ctx context.Context, provider, code, state string) (OAuthTokenSet, error) {
	slog.Info("OAuth token exchange", "provider", provider)

	pcfg, ok := s.getProvider(provider)
	if !ok {
		return OAuthTokenSet{}, fmt.Errorf("不支持该服务的 OAuth 授权")
	}

	if state != "" {
		val, loaded := s.states.LoadAndDelete(state)
		if !loaded {
			return OAuthTokenSet{}, fmt.Errorf("授权状态无效或已过期，请重新授权")
		}
		entry := val.(*oauthStateEntry)
		if entry.isExpired() {
			return OAuthTokenSet{}, fmt.Errorf("授权已超时，请重新授权")
		}
		if entry.Provider != provider {
			return OAuthTokenSet{}, fmt.Errorf("授权提供者不匹配")
		}
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.callbackURL)

	var authHeader string

	if pcfg.IsDynamic {
		// MCP OAuth 2.1: use dynamic client credentials + PKCE
		if dci, ok := s.dynamicClients.Load(provider); ok {
			dc := dci.(*dynamicClientInfo)
			form.Set("client_id", dc.ClientID)
			basic := base64.StdEncoding.EncodeToString([]byte(dc.ClientID + ":" + dc.ClientSecret))
			authHeader = "Basic " + basic
		}
	} else if provider == "supabase" {
		basic := base64.StdEncoding.EncodeToString([]byte(pcfg.ClientID + ":" + pcfg.ClientSecret))
		authHeader = "Basic " + basic
	} else {
		form.Set("client_id", pcfg.ClientID)
		form.Set("client_secret", pcfg.ClientSecret)
	}

	if state != "" {
		if cv, loaded := s.pkceStore.LoadAndDelete(state); loaded {
			form.Set("code_verifier", cv.(string))
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", pcfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthTokenSet{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LiteFlow-Backend/1.0")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return OAuthTokenSet{}, fmt.Errorf("OAuth 令牌交换失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return OAuthTokenSet{}, fmt.Errorf("read token response: %w", err)
	}

	slog.Info("OAuth token response", "provider", provider, "status", resp.StatusCode)

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return OAuthTokenSet{}, fmt.Errorf("解析授权响应失败")
	}

	tokenSet, err := parseTokenSet(result)
	if err != nil {
		return OAuthTokenSet{}, err
	}
	slog.Info("OAuth token received", "provider", provider, "token_len", len(tokenSet.AccessToken))
	return tokenSet, nil
}

func (s *OAuthService) RefreshAccessToken(ctx context.Context, provider, refreshToken string) (OAuthTokenSet, error) {
	if refreshToken == "" {
		return OAuthTokenSet{}, fmt.Errorf("refresh_token 为空")
	}
	pcfg, ok := s.getProvider(provider)
	if !ok {
		return OAuthTokenSet{}, fmt.Errorf("不支持该服务的 OAuth 授权")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	var authHeader string
	if pcfg.IsDynamic {
		if dci, ok := s.dynamicClients.Load(provider); ok {
			dc := dci.(*dynamicClientInfo)
			form.Set("client_id", dc.ClientID)
			if dc.ClientSecret != "" {
				basic := base64.StdEncoding.EncodeToString([]byte(dc.ClientID + ":" + dc.ClientSecret))
				authHeader = "Basic " + basic
			}
		} else {
			dci, err := s.ensureDynamicClient(provider, pcfg)
			if err != nil {
				return OAuthTokenSet{}, fmt.Errorf("动态客户端注册失败: %w", err)
			}
			form.Set("client_id", dci.ClientID)
			if dci.ClientSecret != "" {
				basic := base64.StdEncoding.EncodeToString([]byte(dci.ClientID + ":" + dci.ClientSecret))
				authHeader = "Basic " + basic
			}
		}
	} else if provider == "supabase" {
		basic := base64.StdEncoding.EncodeToString([]byte(pcfg.ClientID + ":" + pcfg.ClientSecret))
		authHeader = "Basic " + basic
	} else {
		form.Set("client_id", pcfg.ClientID)
		form.Set("client_secret", pcfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", pcfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthTokenSet{}, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LiteFlow-Backend/1.0")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return OAuthTokenSet{}, fmt.Errorf("刷新 access token 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return OAuthTokenSet{}, fmt.Errorf("read refresh response: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return OAuthTokenSet{}, fmt.Errorf("解析刷新响应失败")
	}

	tokenSet, err := parseTokenSet(result)
	if err != nil {
		return OAuthTokenSet{}, err
	}
	if tokenSet.RefreshToken == "" {
		tokenSet.RefreshToken = refreshToken
	}
	return tokenSet, nil
}

// ensureDynamicClient performs MCP OAuth 2.1 dynamic client registration if needed.
func (s *OAuthService) ensureDynamicClient(provider string, pcfg *providerConfig) (*dynamicClientInfo, error) {
	if cached, ok := s.dynamicClients.Load(provider); ok {
		dc := cached.(*dynamicClientInfo)
		if time.Since(dc.RegisteredAt) < time.Hour {
			return dc, nil
		}
	}

	regBody := map[string]any{
		"client_name":                "LiteFlow",
		"redirect_uris":              []string{s.callbackURL},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "client_secret_basic",
	}

	bodyBytes, _ := json.Marshal(regBody)
	req, err := http.NewRequest("POST", pcfg.RegisterURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dynamic registration request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("dynamic registration HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse registration response: %w", err)
	}

	clientID, _ := result["client_id"].(string)
	clientSecret, _ := result["client_secret"].(string)
	if clientID == "" {
		return nil, fmt.Errorf("dynamic registration returned no client_id")
	}

	dci := &dynamicClientInfo{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RegisteredAt: time.Now(),
	}
	s.dynamicClients.Store(provider, dci)
	slog.Info("Dynamic client registered", "provider", provider, "clientId", clientID)
	return dci, nil
}

func generateCodeVerifier() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func parseTokenSet(result map[string]any) (OAuthTokenSet, error) {
	if errField, ok := result["error"].(string); ok && errField != "" {
		desc, _ := result["error_description"].(string)
		if desc == "" {
			desc = errField
		}
		return OAuthTokenSet{}, fmt.Errorf("OAuth 授权失败: %s", desc)
	}

	accessToken, _ := result["access_token"].(string)
	if accessToken == "" {
		return OAuthTokenSet{}, fmt.Errorf("未获取到访问令牌")
	}
	refreshToken, _ := result["refresh_token"].(string)

	var expiresIn int64
	switch v := result["expires_in"].(type) {
	case float64:
		expiresIn = int64(v)
	case int64:
		expiresIn = v
	case int:
		expiresIn = int64(v)
	case string:
		if v != "" {
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err == nil {
				expiresIn = parsed
			}
		}
	}

	return OAuthTokenSet{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
	}, nil
}
