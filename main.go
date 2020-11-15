package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func fail(err error) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{
		StatusCode: 500,
		Body:       err.Error(),
	}, err
}

// Register the url to POST when the Google Doc changes
func watch(ctx context.Context, fileID string) error {
	data := []byte(os.Getenv("JSON_KEY"))
	conf, err := google.JWTConfigFromJSON(data, "https://www.googleapis.com/auth/drive.readonly")
	if err != nil {
		return err
	}

	client := conf.Client(ctx)
	res, err := client.Post(fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s/watch", os.Getenv("FILE_ID")), "application/json",
		strings.NewReader(fmt.Sprintf(`{
			"id": "%s",
			"type": "web_hook",
			"address": "%s"
		}`, os.Getenv("CHANNEL_ID"), os.Getenv("MYDOMAIN_URL"))))
	if err != nil || res.StatusCode == 200 {
		return err
	}
	type GAPIError struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	bytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode == 400 {
		var gapiError GAPIError
		json.Unmarshal(bytes, &gapiError)
		if len(gapiError.Error.Errors) > 0 && gapiError.Error.Errors[0].Reason == "channelIdNotUnique" {
			return nil // channel already registered (we assume it's us)
		}
	}
	return errors.New(string(bytes))
}

// HandleRequest refreshes the watch subscription, download the doc as html, commit and push to the repo
func HandleRequest(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	log.Println("subscribing to watch file...")
	if err := watch(ctx, os.Getenv("FILE_ID")); err != nil {
		return fail(err)
	}

	val, ok := request.Headers["x-goog-channel-id"]
	if !ok || val != os.Getenv("CHANNEL_ID") { // this is not for us
		return events.APIGatewayProxyResponse{
			StatusCode: 200,
			Body:       "OK",
		}, nil
	}

	val, ok = request.Headers["x-goog-resource-state"]
	if !ok || val == "sync" { // if it's an "heartbeat" return early
		return events.APIGatewayProxyResponse{
			StatusCode: 200,
			Body:       "OK",
		}, nil
	}

	jsonKey := []byte(os.Getenv("JSON_KEY"))
	conf, err := google.JWTConfigFromJSON(jsonKey, "https://www.googleapis.com/auth/drive.readonly")
	if err != nil {
		return fail(err)
	}

	client := conf.Client(ctx)
	log.Println("getting the doc as an html file...")
	res, err := client.Get(fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s/export?mimeType=text/html", os.Getenv("FILE_ID")))
	if err != nil {
		return fail(err)
	}
	if res.StatusCode != 200 {
		buf := new(strings.Builder)
		_, err := io.Copy(buf, res.Body)
		if err != nil {
			return fail(err)
		}
		return fail(errors.New(buf.String()))
	}

	log.Println("parsing the private key...")
	privkey, err := ssh.ParsePrivateKey([]byte(strings.Join(strings.Split(os.Getenv("SSH_PRIVATE_KEY"), "\\n"), "\n")))
	if err != nil {
		return fail(err)
	}

	log.Println("writing the known_hosts file...")
	f, err := os.Create("/tmp/known_hosts")
	if err != nil {
		return fail(err)
	}
	_, err = f.WriteString(os.Getenv("SSH_KNOWN_HOSTS"))
	if err != nil {
		return fail(err)
	}
	f.Close()

	cb, err := knownhosts.New("/tmp/known_hosts")
	if err != nil {
		return fail(err)
	}
	auth := &gitssh.PublicKeys{User: "git", Signer: privkey, HostKeyCallbackHelper: gitssh.HostKeyCallbackHelper{HostKeyCallback: cb}}

	log.Println("cloning the repo...")
	if _, err := os.Stat("/tmp/website"); !os.IsNotExist(err) {
		os.RemoveAll("/tmp/website")
	}
	repo, err := git.PlainClone("/tmp/website", false, &git.CloneOptions{
		URL:  fmt.Sprintf("git@github.com:%s.git", os.Getenv("REPO_NAME")),
		Auth: auth,
	})
	if err != nil {
		return fail(err)
	}

	log.Println("pulling from the repo...")
	w, err := repo.Worktree()
	if err != nil {
		return fail(err)
	}
	err = w.Pull(&git.PullOptions{RemoteName: "origin", Auth: auth})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fail(err)
	}

	log.Println("writing index.html to the fs...")
	outFile, err := os.Create("/tmp/website/index.html")
	if err != nil {
		return fail(err)
	}
	defer outFile.Close()

	bytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return fail(err)
	}
	htmlDoc := string(bytes)
	fixedHTMLDoc := strings.Replace(htmlDoc, "body style=\"", "body style=\"padding-top: 1rem !important; padding-left: 3rem !important; ", 1)
	_, err = outFile.WriteString(fixedHTMLDoc)
	if err != nil {
		return fail(err)
	}

	log.Println("committing locally...")
	_, err = w.Add("index.html")
	if err != nil {
		return fail(err)
	}
	_, err = w.Commit("automatically pushed from the lambda", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Lambda",
			Email: "lambda@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		return fail(err)
	}

	log.Println("pushing to github...")
	if err := repo.Push(&git.PushOptions{RemoteName: "origin", Auth: auth}); err != nil {
		return fail(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       "OK",
	}, nil
}

func main() {
	// Register for updates during initialization. Then do the same during each lambda call
	if err := watch(oauth2.NoContext, os.Getenv("FILE_ID")); err != nil {
		log.Fatal(err)
	}

	log.Println("Listening...")
	lambda.Start(HandleRequest)
}
