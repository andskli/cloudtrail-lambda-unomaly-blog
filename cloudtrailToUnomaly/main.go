package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/tidwall/gjson"
)

type unomalyPostBody struct {
	Source    string          `json:"source"`
	Timestamp string          `json:"timestamp"`
	Message   string          `json:"message"`
	Metadata  unomalyMetaData `json:"metadata"`
}

type unomalyMetaData struct {
	EventVersion    string `json:"eventVersion"`
	AwsRegion       string `json:"awsRegion"`
	EventSource     string `json:"eventSource"`
	SourceIPAddress string `json:"sourceIPAddress"`
	UserAgent       string `json:"userAgent"`
}

type unomalyCfg struct {
	Endpoint  string
	UseSSL    bool
	BatchSize int
}

func (cfg *unomalyCfg) getEndpointFromEnv() {
	cfg.Endpoint = os.Getenv("UNOMALY_API_ENDPOINT")
}

func (cfg *unomalyCfg) getBatchSizeFromEnv() error {
	size, err := strconv.ParseInt(os.Getenv("UNOMALY_BATCH_SIZE"), 0, 0)
	if err != nil {
		return err
	}
	cfg.BatchSize = int(size)
	return nil
}

var uCfg = new(unomalyCfg)

func postToUnomaly(url string, payload []byte) error {
	p := bytes.NewBuffer(payload)
	req, err := http.NewRequest("POST", url, p)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("BROKEN REQ DEBUG: %v\n", req)
		return errors.New("Wrong status code returned from Unomaly API")
	}

	return nil
}

func handler(ctx context.Context, s3Event events.S3Event) error {
	uCfg := new(unomalyCfg)
	uCfg.getEndpointFromEnv()
	fmt.Println("Unomaly Endpoint: ", uCfg.Endpoint)
	uCfg.getBatchSizeFromEnv()
	fmt.Println("Unomaly Batch Size: ", uCfg.BatchSize)

	svc := s3.New(session.New())

	var events []*unomalyPostBody

	for _, record := range s3Event.Records {
		res, err := svc.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(record.S3.Bucket.Name),
			Key:    aws.String(record.S3.Object.Key),
		})
		if err != nil {
			return err
		}
		defer res.Body.Close()

		buf := new(bytes.Buffer)
		buf.ReadFrom(res.Body)
		var data map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
			return err
		}

		// un-nesting the records we want from the file in S3...
		rcrds := data["Records"].([]interface{})
		for _, r := range rcrds {
			j, err := json.Marshal(r)
			if err != nil {
				return err
			}

			js := string(j)

			eventTime := gjson.Get(js, "eventTime").String()
			userName := gjson.Get(js, "userIdentity.userName").String()
			eventSource := gjson.Get(js, "eventSource").String()
			eventName := gjson.Get(js, "eventName").String()
			requestParameters := gjson.Get(js, "requestParameters").String()
			errorCode := gjson.Get(js, "errorCode").String()
			errorMessage := gjson.Get(js, "errorMessage").String()

			// Maybe request parameters as well?
			var metadata unomalyMetaData
			metadata.EventVersion = gjson.Get(js, "eventVersion").String()
			metadata.EventSource = gjson.Get(js, "eventSource").String()
			metadata.AwsRegion = gjson.Get(js, "awsRegion").String()
			metadata.SourceIPAddress = gjson.Get(js, "sourceIPAddress").String()
			metadata.UserAgent = gjson.Get(js, "userAgent").String()

			msg := fmt.Sprintf("%s %s", eventSource, eventName)
			if errorCode != "" {
				msg = fmt.Sprintf("%s errorCode:%s errorMessage:%s", msg, errorCode, errorMessage)
			}
			if requestParameters != "" {
				msg = fmt.Sprintf("%s requestParameters:%s", msg, requestParameters)
			}

			//msg := fmt.Sprintf("%s %s %s %s %s",
			//	eventSource, eventName, requestParameters, errorCode, errorMessage)
			//msg = strings.TrimSpace(msg)

			e := &unomalyPostBody{
				Source:    userName,
				Message:   strings.TrimSpace(msg),
				Timestamp: eventTime,
				Metadata:  metadata,
				// Metadata:  fmt.Sprintf("%s", metadata),
			}

			// fmt.Printf("MSG TO SEND: %v\n", e)

			if len(userName) > 0 && len(eventTime) > 0 {
				events = append(events, e)
			} else {
				fmt.Println("Event with empty strings: ", e)
			}
		}
	}

	fmt.Println("Number of messages to send: ", len(events))

	for len(events) > 0 {
		fmt.Println("Events left to process: ", len(events))
		chunkSize := uCfg.BatchSize
		if len(events) <= chunkSize {
			chunkSize = len(events)
		}

		take := events[:chunkSize]
		events = events[chunkSize:]
		reqBody, err := json.Marshal(take)
		if err != nil {
			return err
		}

		if err := postToUnomaly(uCfg.Endpoint, reqBody); err != nil {
			return err
		}
		fmt.Printf("Sending %d events to Unomaly endpoint\n", len(take))
	}

	return nil
}

func main() {

	// Ignore certificate check because Unomaly ships with a self signed cert
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	lambda.Start(handler)
}
