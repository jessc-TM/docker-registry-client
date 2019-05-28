package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

type TokenTransport struct {
	Transport http.RoundTripper
	Username  string
	Password  string
}

func (t *TokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if authService := isTokenDemand(resp); authService != nil {
		if resp.Body != nil {
			resp.Body.Close()
		}

		resp, err = t.authAndRetry(authService, req)
	}
	return resp, err
}

type authToken struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

func (t *TokenTransport) authAndRetry(authService *authService, req *http.Request) (*http.Response, error) {
	token, authResp, err := t.auth(authService)
	if err != nil {
		return authResp, err
	}

	retryResp, err := t.retry(req, token)
	return retryResp, err
}

func (t *TokenTransport) auth(authService *authService) (string, *http.Response, error) {
	client := http.Client{
		Transport: t.Transport,
	}

	// Pre-emptively send Basic authentication credentials as some services need them.
	authReq, err := authService.Request(t.Username, t.Password)
	if err != nil {
		return "", nil, err
	}

	response, err := client.Do(authReq)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}

	if err != nil {
		return "", nil, err
	}

	if response.StatusCode != http.StatusOK {
		return "", response, err
	}

	var authToken authToken
	decoder := json.NewDecoder(response.Body)
	err = decoder.Decode(&authToken)
	if err != nil {
		return "", nil, err
	}

	// If we got `{"token":"value"}` then return the token.
	if authToken.Token != "" {
		return authToken.Token, nil, nil
	}

	// If we got `{"access_token":"value"}` then return the token.
	if authToken.AccessToken != "" {
		return authToken.AccessToken, nil, nil
	}

	// Give up here and report an error
	return "", nil, errors.New("unable to extract token")
}

func (t *TokenTransport) retry(req *http.Request, token string) (*http.Response, error) {
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	resp, err := t.Transport.RoundTrip(req)
	return resp, err
}

type authService struct {
	Realm   string
	Service string
	Scope   string
}

func (authService *authService) Request(username, password string) (*http.Request, error) {
	url, err := url.Parse(authService.Realm)
	if err != nil {
		return nil, err
	}

	q := url.Query()
	q.Set("service", authService.Service)
	if authService.Scope != "" {
		q.Set("scope", authService.Scope)
	}
	url.RawQuery = q.Encode()

	request, err := http.NewRequest("GET", url.String(), nil)

	if username != "" || password != "" {
		request.SetBasicAuth(username, password)
	}

	return request, err
}

func isTokenDemand(resp *http.Response) *authService {
	if resp == nil {
		return nil
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return nil
	}
	return parseOauthHeader(resp)
}

func parseOauthHeader(resp *http.Response) *authService {
	challenges := parseAuthHeader(resp.Header)
	for _, challenge := range challenges {
		if challenge.Scheme == "bearer" {
			return &authService{
				Realm:   challenge.Parameters["realm"],
				Service: challenge.Parameters["service"],
				Scope:   challenge.Parameters["scope"],
			}
		}
	}
	return nil
}
