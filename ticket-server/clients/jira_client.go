package clients

import (
	jira "github.com/andygrunwald/go-jira"
	"time"
)

func CreateJiraClient(username, password, url string) (*jira.Client, error) {
	tp := jira.BasicAuthTransport{
		Username: username,
		Password: password,
	}
	ct := tp.Client()
	ct.Timeout = 15 * time.Second
	client, err := jira.NewClient(ct, "https://"+url)
	if err != nil {
		return nil, err
	}

	return client, nil
}
