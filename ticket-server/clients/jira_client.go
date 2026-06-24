package clients

import (
	jira "github.com/andygrunwald/go-jira"
	"time"
)

// jiraHTTPTimeout is the timeout for the Jira HTTP client.
const jiraHTTPTimeout = 15 * time.Second

func CreateJiraClient(username, password, url string) (*jira.Client, error) {
	tp := jira.BasicAuthTransport{
		Username: username,
		Password: password,
	}
	ct := tp.Client()
	ct.Timeout = jiraHTTPTimeout
	client, err := jira.NewClient(ct, "https://"+url)
	if err != nil {
		return nil, err
	}

	return client, nil
}
