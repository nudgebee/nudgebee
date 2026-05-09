package core

import (
	"net/url"
	"nudgebee/llm/config"
	"path"
	"strings"
)

func GetNudgebeeUIReferenceForClusterDetails(
	ctx NbToolContext,
	modules []string,
	text string,
	queryParameters map[string]string,
	query string,
) NBToolResponseReference {

	baseUrl := config.Config.BaseUrl
	if baseUrl == "" {
		baseUrl = "http://localhost:3000"
	}
	if !strings.HasSuffix(baseUrl, "/") {
		baseUrl = baseUrl + "/"
	}

	u, err := url.Parse(baseUrl)
	if err != nil {

		return NBToolResponseReference{}
	}

	// Build path
	u.Path = path.Join(u.Path, "kubernetes/details", ctx.AccountId)

	// Add query parameters
	q := u.Query()
	for k, v := range queryParameters {
		q.Set(k, v)
	}

	// Add `query` as a query parameter
	if query != "" {
		q.Set("query", query)
	}

	u.RawQuery = q.Encode()

	// Add fragment/hash
	if hash := strings.Join(modules, "/"); hash != "" {
		u.Fragment = hash
	}

	ref := NBToolResponseReference{
		Text:  text,
		Url:   u.String(),
		Query: query,
	}

	if query != "" {
		GenerateReferenceTitleAsync(ctx, ref, query)
	}

	return ref
}

func GetNudgebeeUIReference(ctx NbToolContext, page string, text string, queryParameters map[string]string, query string) NBToolResponseReference {
	url := config.Config.BaseUrl
	if url == "" {
		url = "http://localhost:3000"
	}
	if !strings.HasSuffix(url, "/") {
		url = url + "/"
	}

	url = url + page
	if len(queryParameters) > 0 {
		url = url + "?"
		for k, v := range queryParameters {
			url = url + k + "=" + v + "&"
		}
	}
	ref := NBToolResponseReference{
		Text:  text,
		Url:   url,
		Query: query,
	}

	if query != "" {
		GenerateReferenceTitleAsync(ctx, ref, query)
	}

	return ref
}
