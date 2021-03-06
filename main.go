// This is a lambda function to support objects duplication among AWS S3 buckets
// Configuration can support many to many duplication. However, this function checks and prevent
// the cyclic duplication based on the configuration
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/mitchellh/mapstructure"
)

// Config Describe the structure of configuration for one source bucket
type Config struct {
	Region       string   `json:"region"`
	SQS          string   `json:"sqs"`
	SQSRegion    string   `json:"sqsRegion"`
	Destinations []string `json:"destinations"`
}

var config map[string]Config

func parseConfig() (err error) {
	config = make(map[string]Config)
	data := make([]byte, 0)
	configURL := os.Getenv("CONFIG_FILE")
	if configURL != "" {
		resp, err := http.Get(configURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		data, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
	} else {
		data, err = base64.StdEncoding.DecodeString(os.Getenv("CONFIG"))
		if err != nil {
			return err
		}
	}

	if len(data) == 0 {
		return fmt.Errorf("no configuration available")
	}

	err = json.Unmarshal(data, &config)
	if err != nil {
		return err
	}

	PanicIfCopyCycleFound()

	return nil
}

// PanicIfCopyCycleFound detect the cyclic duplication in configuration and panic if any such instance is found
func PanicIfCopyCycleFound() {
	for k, v := range config {
		visitedMap := make(map[string]bool)
		visitedMap[k] = true
		cycle := k
		detectCycle(v.Destinations, cycle, visitedMap)
	}
}

func detectCycle(conf []string, cycle string, visitedMap map[string]bool) {
	for _, d := range conf {
		branch := cycle + " -> " + d
		if visitedMap[d] {
			log.Fatal("Cyclic copy found: " + branch)
		} else {
			visitedMap[d] = true
			if len(config[d].Destinations) != 0 {
				detectCycle(config[d].Destinations, branch, visitedMap)
			}
		}
	}
}

func processS3Event(request events.S3Event) (err error) {
	errChan := make(chan error)
	for _, v := range request.Records {
		log.Println("Moving", v.S3.Bucket.Name, v.S3.Object.Key, "To", config[v.S3.Bucket.Name].Destinations)
		sess, err := session.NewSession(&aws.Config{Region: aws.String(config[v.S3.Bucket.Name].Region)})
		if err != nil {
			return fmt.Errorf("unable to enstablish aws session for %v", config[v.S3.Bucket.Name])
		}
		for _, v1 := range config[v.S3.Bucket.Name].Destinations {
			go copyObjects(s3.New(sess), v.S3.Bucket.Name, v1, v.S3.Object.Key, errChan)
		}
	}

	for _, v := range request.Records {
		for range config[v.S3.Bucket.Name].Destinations {
			err = <-errChan
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func processSQSMessage() (err error) {
	for k, v := range config {
		if v.SQS == "" {
			log.Println("No SQS found for", k)
			continue
		}
		s := sqs.New(session.Must(session.NewSession()), aws.NewConfig().WithRegion(v.SQSRegion))
		var wg sync.WaitGroup
		for {
			r, err := s.ReceiveMessage(&sqs.ReceiveMessageInput{
				QueueUrl:            aws.String(v.SQS),
				MaxNumberOfMessages: aws.Int64(int64(10)),
			})

			if err != nil {
				return fmt.Errorf("error while reading from SQS: %v", err)
			}

			if len(r.Messages) == 0 {
				break
			}

			wg.Add(1)
			go processSQSEvent(&wg, s, r, v.SQS)
		}
		wg.Wait()
	}

	return nil
}

func processSQSEvent(wg *sync.WaitGroup, s *sqs.SQS, receiveResp *sqs.ReceiveMessageOutput, sqsURL string) {
	defer wg.Done()

	for _, message := range receiveResp.Messages {
		var snsMessages events.SNSEntity
		if err := json.Unmarshal([]byte(*message.Body), &snsMessages); err != nil {
			log.Printf("error while unmarshaling SNS json %v %v", err, *message.Body)
			continue
		}

		var s3Event events.S3Event
		if err := json.Unmarshal([]byte(snsMessages.Message), &s3Event); err != nil {
			//Support message coming directly to SQS from S3
			if err = json.Unmarshal([]byte(*message.Body), &s3Event); err != nil {
				log.Printf("error while unmarshaling SQS json %v %v", err, *message.Body)
				continue
			}
		}

		if err := processS3Event(s3Event); err != nil {
			log.Printf("error while processing s3 event via SQS %v", err)
			continue
		}

		// Delete message
		if err := deleteMessageFromSQS(s, message, sqsURL); err != nil {
			log.Println("error occured during deleting message from SQS. ", err, message)
		}

	}
}

func processSNSEvent(receiveResp *events.SNSEvent) error {
	for _, message := range receiveResp.Records {
		var s3Event events.S3Event
		if err := json.Unmarshal([]byte(message.SNS.Message), &s3Event); err != nil {
			return fmt.Errorf("error while unmarshaling SNS event %v %v", err, message.SNS.Message)
		}

		if err := processS3Event(s3Event); err != nil {
			return fmt.Errorf("error while processing s3 event via SNS %v", err)
		}
	}
	return nil
}

func deleteMessageFromSQS(svc *sqs.SQS, message *sqs.Message, QueueURL string) error {
	deleteParams := &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(QueueURL),  // Required
		ReceiptHandle: message.ReceiptHandle, // Required
	}
	_, err := svc.DeleteMessage(deleteParams)
	if err != nil {
		return err
	}

	return err
}

// ProcessIncomingEvents Handles an individual S3 event
// S3 events could be received in following way
// - Trigger Lambda function directly from S3 event
// - Trigger Lambda function from SNS which receive the notification on every S3 event
// - Trigger Lambda function from CloudWatch periodically and provide a SQS in config which will to be checked on invocation
func ProcessIncomingEvents(event interface{}) error {
	err := parseConfig()
	if err != nil {
		return fmt.Errorf("error in parsing the config %v", err)
	}

	e, s3Event := event.(map[string]interface{}), events.S3Event{}

	if mapstructure.Decode(e, &s3Event); len(s3Event.Records) > 0 && s3Event.Records[0].S3.Object.Key != "" {
		log.Println("Got S3 Event")
		return processS3Event(s3Event)
	}

	snsEvent := events.SNSEvent{}
	if mapstructure.Decode(e, &snsEvent); len(snsEvent.Records) > 0 && snsEvent.Records[0].SNS.MessageID != "" {
		log.Println("Got S3 Event")
		return processSNSEvent(&snsEvent)
	}

	log.Println("Defaulting to SQS")
	return processSQSMessage()
}

func copyObjects(svc *s3.S3, from, to, item string, errChan chan error) {
	_, err := svc.CopyObject(&s3.CopyObjectInput{Bucket: aws.String(to), CopySource: aws.String(from + "/" + item),
		Key: aws.String(item)})
	if err != nil {
		errChan <- fmt.Errorf("unable to copy item %s from bucket %q to bucket %q, %v", item, from, to, err)
		return
	}

	err = svc.WaitUntilObjectExists(&s3.HeadObjectInput{Bucket: aws.String(to), Key: aws.String(item)})
	if err != nil {
		errChan <- fmt.Errorf("error occurred while waiting for item %q to be copied to bucket %q, %v",
			item, to, err)
		return
	}
	errChan <- nil
}

func main() {
	lambda.Start(ProcessIncomingEvents)
}
