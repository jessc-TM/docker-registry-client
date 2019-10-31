package registry

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	ErrNoMorePages = errors.New("No more pages")
)

func (registry *Registry) getJson(u string, response interface{}) error {
	resp, err := registry.Client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(response)
	if err != nil {
		return err
	}

	return nil
}

// getPaginatedJson accepts a string and a pointer, and returns the
// next page URL while updating pointed-to variable with a parsed JSON
// value. When there are no more pages it returns `ErrNoMorePages`.
func (registry *Registry) getPaginatedJson(u string, response interface{}) (string, error) {
	resp, err := registry.Client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(response)
	if err != nil {
		return "", err
	}
	return getNextLink(u, resp)
}

// Matches an RFC 5988 (https://tools.ietf.org/html/rfc5988#section-5)
// Link header. For example,
//
//    <http://registry.example.com/v2/_catalog?n=5&last=tag5>; type="application/json"; rel="next"
//
// The URL is _supposed_ to be wrapped by angle brackets `< ... >`,
// but e.g., quay.io does not include them. Similarly, params like
// `rel="next"` may not have quoted values in the wild.
var nextLinkRE = regexp.MustCompile(` *<?([^;>]+)>? *(?:;[^;]*)*; *rel="?next"?`)

func getNextLink(base string, resp *http.Response) (string, error) {
	for _, link := range resp.Header[http.CanonicalHeaderKey("Link")] {
		for _, l := range strings.Split(link, ",") {
			parts := nextLinkRE.FindStringSubmatch(l)
			if parts != nil {
				baseURL, _ := url.Parse(base)
				linkURL, _ := url.Parse(parts[1])

				return baseURL.ResolveReference(linkURL).String(), nil
			}
		}
	}

	// DTR now uses an `X-Next-Page-Start` header instead of the standard `Link` header;
	// the value is meant to be used in the `pageStart` query parameter for the next request.
	if nextPage := resp.Header.Get("X-Next-Page-Start"); nextPage != "" {
		baseURL, _ := url.Parse(base)
		q := baseURL.Query()
		q.Set("pageStart", nextPage)
		baseURL.RawQuery = q.Encode()

		return baseURL.String(), nil
	}

	return "", ErrNoMorePages
}
