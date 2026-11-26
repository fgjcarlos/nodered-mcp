package nodered

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/lock"
)

var errCommsAuthRejected = errors.New("authentication rejected by Node-RED (token invalid or lacking status.read)")

var errAdminCredentials = errors.New("Node-RED rejected the configured username or password")

type adminToken struct {
	value   string
	expires time.Time
	origin  *url.URL
}

type passwordAuth struct {
	username, password string
	mu                 lock.CtxMutex
	loginRequired      *bool
	loginOrigin        *url.URL
	token              *adminToken
}

func safeAdminRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 || req.URL.User != nil {
		return http.ErrUseLastResponse
	}
	from, to := via[len(via)-1].URL, req.URL
	if from.Scheme == to.Scheme && strings.EqualFold(from.Host, to.Host) {
		return nil
	}
	if from.Scheme == "http" && to.Scheme == "https" && strings.EqualFold(from.Hostname(), to.Hostname()) {
		return nil
	}
	return http.ErrUseLastResponse
}

func (a *passwordAuth) bearer(ctx context.Context, c *Client) (*adminToken, error) {
	if err := a.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer a.mu.Unlock()
	if a.loginRequired == nil {
		var login map[string]json.RawMessage
		origin, err := c.adminAuthJSON(ctx, http.MethodGet, "/auth/login", nil, nil, &login)
		if err != nil {
			return nil, err
		}
		if login == nil || (len(login) != 0 && string(login["type"]) != `"credentials"`) {
			return nil, errors.New("Node-RED returned an unsupported admin login scheme")
		}
		required := len(login) != 0
		a.loginRequired = &required
		a.loginOrigin = origin
	}
	if !*a.loginRequired {
		return nil, nil
	}
	if a.token != nil && (a.token.expires.IsZero() || time.Now().Before(a.token.expires)) {
		return a.token, nil
	}
	var grant struct {
		Token     string `json:"access_token"`
		ExpiresIn *int64 `json:"expires_in"`
	}
	form, _ := json.Marshal(map[string]string{"client_id": "node-red-admin", "grant_type": "password", "scope": "*", "username": a.username, "password": a.password})
	started := time.Now()
	if _, err := c.adminAuthJSON(ctx, http.MethodPost, "/auth/token", a.loginOrigin, bytes.NewReader(form), &grant); err != nil {
		return nil, err
	}
	if strings.TrimSpace(grant.Token) == "" || strings.ContainsAny(grant.Token, " \t\r\n") {
		return nil, errors.New("Node-RED returned an invalid admin access token")
	}
	token := &adminToken{value: grant.Token, origin: a.loginOrigin}
	if grant.ExpiresIn != nil {
		if *grant.ExpiresIn < 0 || *grant.ExpiresIn > int64(math.MaxInt64/time.Second) {
			return nil, errors.New("Node-RED returned an invalid admin token lifetime")
		}
		token.expires = started.Add(time.Duration(*grant.ExpiresIn) * time.Second)
	}
	a.token = token
	return token, nil
}

func (a *passwordAuth) invalidate(ctx context.Context, token *adminToken) {
	// Compare the lease, not its string: an old 401 must not evict a newer grant.
	if a.mu.LockContext(ctx) != nil {
		return
	}
	defer a.mu.Unlock()
	if a.token == token {
		a.token = nil
	}
}

func (c *Client) adminAuthJSON(ctx context.Context, method, path string, origin *url.URL, body io.Reader, out any) (*url.URL, error) {
	u, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("building Node-RED %s request", path)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("building Node-RED %s request", path)
	}
	if origin != nil {
		req.URL.Scheme, req.URL.Host, req.Host = origin.Scheme, origin.Host, origin.Host
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("cannot reach Node-RED %s", path)
	}
	defer resp.Body.Close()
	// Node-RED 5 returns 403 (invalid_grant) for a rejected password or scope.
	if method == http.MethodPost && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		return nil, errAdminCredentials
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Node-RED %s returned HTTP %d", path, resp.StatusCode)
	}
	// Auth responses are untrusted and may echo credentials. Never include them in errors.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return nil, fmt.Errorf("invalid Node-RED %s response", path)
	}
	return resp.Request.URL, nil
}

func (c *Client) sendAdmin(req *http.Request, transportMessage string) (*http.Response, error) {
	a := c.passwordAuth
	var lease *adminToken
	for attempt := 0; ; attempt++ {
		if a != nil {
			var err error
			lease, err = a.bearer(req.Context(), c)
			if err != nil {
				return nil, err
			}
			if lease != nil {
				// Honor a TLS upgrade discovered by the anonymous login probe before attaching a token.
				req.URL.Scheme, req.URL.Host, req.Host = lease.origin.Scheme, lease.origin.Host, lease.origin.Host
				req.Header.Set("Authorization", "Bearer "+lease.value)
			}
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, &TransportError{Err: fmt.Errorf("%s: %w", transportMessage, redactedWrap(err))}
		}
		if lease == nil || resp.StatusCode != http.StatusUnauthorized {
			return resp, nil
		}
		a.invalidate(req.Context(), lease)
		if attempt != 0 {
			return resp, nil
		}
		_ = resp.Body.Close()
		req = req.Clone(req.Context())
		if req.GetBody != nil {
			req.Body, err = req.GetBody()
			if err != nil {
				return nil, err
			}
		}
	}
}

func (c *Client) adminErrorBody(resp *http.Response, body []byte) string {
	text := string(body)
	if c.passwordAuth != nil {
		text = redactAdminValue(text, c.passwordAuth.password)
	}
	if resp.Request != nil {
		text = redactAdminValue(text, strings.TrimPrefix(resp.Request.Header.Get("Authorization"), "Bearer "))
	}
	return text
}

func redactAdminValue(text, value string) string {
	if value == "" {
		return text
	}
	quoted, _ := json.Marshal(value)
	for _, form := range []string{string(quoted[1 : len(quoted)-1]), url.QueryEscape(value), value} {
		text = strings.ReplaceAll(text, form, "[redacted]")
	}
	return text
}

func tailAdminToken(ctx context.Context, c *Client, fallback string) (string, *adminToken, error) {
	if c == nil || c.passwordAuth == nil {
		return fallback, nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	lease, err := c.passwordAuth.bearer(ctx, c)
	if lease == nil {
		return "", nil, err
	}
	return lease.value, lease, nil
}

func sanitizeTailAuthError(err *error, token, password string) {
	if *err == nil {
		return
	}
	msg := (*err).Error()
	if clean := redactAdminValue(redactAdminValue(msg, token), password); clean != msg {
		*err = errors.New(clean)
	}
}

func tailAdminURL(raw string, lease *adminToken) string {
	if lease == nil || lease.origin.Scheme != "https" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme, u.Host = "wss", lease.origin.Host
	return u.String()
}
