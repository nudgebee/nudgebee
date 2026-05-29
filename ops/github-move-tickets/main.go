package main

// added a test change which does absolutely nothing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"os"
	"strings"
)

var token = os.Getenv("GITHUB_PROJECT_TOKEN")

const (
	sourceProjectID  = "PVT_kwDOCG7t1c4ATt4G"
	destProjectID    = "PVT_kwDOCG7t1c4AasYt"
	sourceStatus     = "✅ Done"
	destContentType  = "Done"
	githubAPIBaseURL = "https://api.github.com/graphql"
)

func main() {

	// 0. Get all projects
	// curl --request POST \
	// --url https://api.github.com/graphql \
	// --header 'Authorization: Bearer xxx' \
	// --data '{"query":"query{organization(login: \"nudgebee\") {projectV2(number: 3){id}}}"}'

	// 1. Get all issues from source project
	//  curl --request POST \
	// --url https://api.github.com/graphql \
	//--header 'Authorization: Bearer xxx' \
	//--data '{"query":"query getIssueDetailsOnProject { node (id: \"PVT_kwDOCG7t1c4ATt4G\") { ... on ProjectV2 { number title shortDescription items( first: 50) { totalCount pageInfo { endCursor hasNextPage startCursor } nodes { id content { ... on Issue { id number } ... on DraftIssue { id } } fieldValueByName( name: \"Status\") { ... on ProjectV2ItemFieldSingleSelectValue { status: name } }}}}}}"}'
	err := ProcessProjectIssues()
	if err != nil {
		panic(err)
	}
}
func ProcessProjectIssues() error {
	httpClient := &http.Client{}
	contentsToMove := map[string]string{}
	nodeIdsToDelete := []string{}
	// Get issue IDs for the source project
	issueIDs, err := getIssueIDs(sourceProjectID)
	if err != nil {
		return err
	}
	fmt.Println("Issue IDs: ", issueIDs)

	var hasNextPage bool
	endCursor := ""
	for {
		cursorValue := "null"
		if endCursor != "" {
			cursorValue = fmt.Sprintf(`\"%s\"`, endCursor)
		}
		req, err := http.NewRequest("POST", githubAPIBaseURL, strings.NewReader(fmt.Sprintf(`{"query":"query getIssueDetailsOnProject { node (id: \"%s\") { ... on ProjectV2 { number title shortDescription items( first: 100, after: %s) { totalCount pageInfo { endCursor hasNextPage startCursor } nodes { id content { ...on PullRequest { id title} ... on Issue { id number } ... on DraftIssue { id } } fieldValueByName( name: \"Status\") { ... on ProjectV2ItemFieldSingleSelectValue { status: name } }}}}}}"}"`, sourceProjectID, cursorValue)))
		if err != nil {
			return err
		}
		req.Header.Add("Authorization", "Bearer "+token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}

		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		if resp.StatusCode != 200 {
			return fmt.Errorf("error: %s %s", resp.Status, string(respBody))
		}

		projectItems := map[string]any{}
		err = json.Unmarshal(respBody, &projectItems)
		if err != nil {
			return err
		}

		fmt.Println(string(respBody))

		if projectItems["errors"] != nil && len(projectItems["errors"].([]any)) > 0 {
			return fmt.Errorf("error: %v", projectItems["errors"])
		}

		nodeItems := projectItems["data"].(map[string]any)["node"].(map[string]any)["items"].(map[string]any)
		if nodeItems["pageInfo"] != nil {
			pageInfo := nodeItems["pageInfo"].(map[string]any)
			if pageInfo["hasNextPage"] == true {
				fmt.Println("Has next page")
				endCursor = pageInfo["endCursor"].(string)
				hasNextPage = true
			} else {
				hasNextPage = false
			}
		} else {
			hasNextPage = false
		}

		nodes := nodeItems["nodes"].([]any)

		for _, node := range nodes {
			nodeMap := node.(map[string]any)
			if nodeMap["content"] == nil {
				continue
			}
			content := nodeMap["content"].(map[string]any)
			if nodeMap["fieldValueByName"] == nil {
				continue
			}
			fieldValueByName := nodeMap["fieldValueByName"].(map[string]any)
			if fieldValueByName["status"] != sourceStatus {
				continue
			}
			// Check if the status is closed
			if fieldValueByName["status"] == "closed" {
				fmt.Println("The issue is closed:", nodeMap["id"], content["id"], fieldValueByName["status"])
			} else {
				fmt.Println("The issue is not closed:", nodeMap["id"], content["id"], fieldValueByName["status"])
				err := closeIssue(content["id"].(string))
				if err != nil {
					nodeID, _ := nodeMap["id"].(string)
					contentID, _ := content["id"].(string)
					fmt.Println("unable to close issue", nodeID, contentID)
				}
			}
			if issueNumber, ok := content["number"]; ok {

				contentsToMove[content["id"].(string)] = fmt.Sprint(issueNumber.(float64))
			} else {
				contentsToMove[content["id"].(string)] = "0"
			}

			nodeIdsToDelete = append(nodeIdsToDelete, nodeMap["id"].(string))
		}

		if !hasNextPage {
			break
		}
	}
	fmt.Println("moving items", len(contentsToMove))

	// 2. Cretae issue on destination project
	// curl --request POST \
	// --url https://api.github.com/graphql \
	// --header 'Authorization: Bearer xxx' \
	// --data '{"query":"mutation {addProjectV2ItemById(input: {projectId: \"PVT_kwDOCG7t1c4AasYt\" contentId: \"PVTI_lADOCG7t1c4ATt4GzgIYCsM\"}) {item {id}}}"}'

	for _, content := range contentsToMove {
		req, err := http.NewRequest("POST", githubAPIBaseURL, strings.NewReader(fmt.Sprintf(`{"query":"mutation {addProjectV2ItemById(input: {projectId: \"%s\" contentId: \"%s\"}) {item {id}}}"}"`, destProjectID, content)))
		if err != nil {
			return err
		}
		req.Header.Add("Authorization", "Bearer "+token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}

		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		if resp.StatusCode != 200 {
			return fmt.Errorf("error: %s %s ", resp.Status, string(respBody))

		}

		// close issue
		// if issueId != "0" {
		// 	fmt.Println("closing issue " + issueId)
		// 	closeIssue(issueId)
		// }

		fmt.Println(string(respBody))

	}

	// 3. Delete issue from source project
	// curl --request POST \
	// --url https://api.github.com/graphql \
	// --header 'Authorization: Bearer xxx' \
	// --data '{"query":"mutation {deleteProjectV2Item(input: {projectId: \"PVT_kwDOCG7t1c4ATt4G\" itemId: \"PVTI_lADOCG7t1c4ATt4GzgIYCsM\"}) {deletedItemId}}"}'
	fmt.Println("deleting items", len(nodeIdsToDelete))

	for _, content := range nodeIdsToDelete {
		req, err := http.NewRequest("POST", githubAPIBaseURL, strings.NewReader(fmt.Sprintf(`{"query":"mutation {deleteProjectV2Item(input: {projectId: \"%s\" itemId: \"%s\"}) {deletedItemId}}"}"`, sourceProjectID, content)))
		if err != nil {
			return (err)
		}
		req.Header.Add("Authorization", "Bearer "+token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return (err)
		}

		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return (err)
		}

		if resp.StatusCode != 200 {

			return fmt.Errorf("error: %s %s ", resp.Status, string(respBody))

		}

		fmt.Println(string(respBody))
	}
	return nil
}

func closeIssue(issueID string) error {
	httpClient := &http.Client{}
	closeIssueMutation := fmt.Sprintf(`{"query":"mutation { closeIssue(input: { issueId: \"%s\" }) { issue { id } }}", "variables":{}}`, issueID)
	req, err := http.NewRequest("POST", githubAPIBaseURL, strings.NewReader(closeIssueMutation))
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		fmt.Println("Error: ", resp.Status, string(respBody))
		return errors.New(string(respBody))
	}
	var result map[string]interface{}
	err = json.Unmarshal(respBody, &result)
	if err != nil {
		return err
	}

	if errors, ok := result["errors"]; ok {
		fmt.Println("GraphQL Errors: ", errors)
		return fmt.Errorf("GraphQL Errors: %v", errors)
	}

	fmt.Println("Closed issue:", string(respBody))

	return nil
}

// Function to identify issue IDs related to a project
func getIssueIDs(projectID string) ([]string, error) {
	httpClient := &http.Client{}
	issueIDs := []string{}
	var hasNextPage bool
	endCursor := ""

	for {
		cursorValue := "null"
		if endCursor != "" {
			cursorValue = fmt.Sprintf(`\"%s\"`, endCursor)
		}
		query := fmt.Sprintf(`{"query":"query getIssues { node(id: \"%s\") { ... on ProjectV2 { items(first: 100, after: %s) { totalCount pageInfo { endCursor hasNextPage startCursor } nodes { content { ... on Issue { id number } } } } } } }"}`, projectID, cursorValue)
		req, err := http.NewRequest("POST", githubAPIBaseURL, strings.NewReader(query))
		if err != nil {
			return nil, err
		}
		req.Header.Add("Authorization", "Bearer "+token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("error: %s %s", resp.Status, string(respBody))
		}

		projectItems := map[string]any{}
		err = json.Unmarshal(respBody, &projectItems)
		if err != nil {
			return nil, err
		}

		if projectItems["errors"] != nil && len(projectItems["errors"].([]any)) > 0 {
			return nil, fmt.Errorf("error: %v", projectItems["errors"])
		}

		nodeItems := projectItems["data"].(map[string]any)["node"].(map[string]any)["items"].(map[string]any)
		if nodeItems["pageInfo"] != nil {
			pageInfo := nodeItems["pageInfo"].(map[string]any)
			if pageInfo["hasNextPage"] == true {
				endCursor = pageInfo["endCursor"].(string)
				hasNextPage = true
			} else {
				hasNextPage = false
			}
		} else {
			hasNextPage = false
		}

		nodes := nodeItems["nodes"].([]any)
		for _, node := range nodes {

			content := node.(map[string]any)["content"].(map[string]any)
			if issueID, ok := content["id"].(string); ok {
				issueIDs = append(issueIDs, issueID)
			}

			if issueID, ok := content["number"].(string); ok {
				issueIDs = append(issueIDs, issueID)
			}
		}

		if !hasNextPage {
			break
		}
	}

	return issueIDs, nil
}
