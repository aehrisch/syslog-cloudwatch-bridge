package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
	"sort"
	"encoding/json"


	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/satori/go.uuid"

	"gopkg.in/mcuadros/go-syslog.v2"
	"gopkg.in/mcuadros/go-syslog.v2/format"
)

var port = os.Getenv("PORT")
var logGroupName = os.Getenv("LOG_GROUP_NAME")
var streamName, err = uuid.NewV4()
var sequenceToken = ""

var (
	client *http.Client
	pool   *x509.CertPool
)

func init() {
	pool = x509.NewCertPool()
	pool.AppendCertsFromPEM(pemCerts)
}

func main() {

	useJson := flag.Bool("json", false, "send events in JSON format")
	flag.Parse()

	if logGroupName == "" {
		log.Fatal("LOG_GROUP_NAME must be specified")
	}

	if port == "" {
		port = "514"
	}

	address := fmt.Sprintf("0.0.0.0:%v", port)
	log.Println("Starting syslog server on", address)
	log.Println("Logging to group:", logGroupName)
	initCloudWatchStream()

	channel := make(syslog.LogPartsChannel, 100)
	handler := syslog.NewChannelHandler(channel)

	server := syslog.NewServer()
	server.SetFormat(syslog.Automatic)
	server.SetHandler(handler)
	server.ListenUDP(address)
	server.ListenTCP(address)

	server.Boot()

	go func(channel syslog.LogPartsChannel) {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop() // release when done, if we ever will

		loglist := make([]format.LogParts, 0)
		for {
			select {
				case <- ticker.C:
					if len(loglist) <= 0 {
						continue
					}
					sendToCloudWatch(*useJson, loglist)
					loglist = make([]format.LogParts, 0)
				case logParts := <- channel:
					loglist = append(loglist, logParts)
			}
		}
	}(channel)

	server.Wait()
}

func sendToCloudWatch(useJson bool, buffer []format.LogParts) {
	// service is defined at run time to avoid session expiry in long running processes
	var svc = cloudwatchlogs.New(session.New())
	// set the AWS SDK to use our bundled certs for the minimal container (certs from CoreOS linux)
	svc.Config.HTTPClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}

	params := &cloudwatchlogs.PutLogEventsInput{
		LogGroupName:  aws.String(logGroupName),
		LogStreamName: aws.String(streamName.String()),
	}

	sort.Slice(buffer, func(i, j int) bool { return buffer[i]["timestamp"].(time.Time).Before(buffer[j]["timestamp"].(time.Time)) })

	for _, logPart := range buffer {

		var event_str = ""
		if useJson {
			json_bytes,_ := json.Marshal(logPart)
			event_str = string(json_bytes)
		} else {
			event_str = logPart["content"].(string)
		}
		params.LogEvents = append(params.LogEvents, &cloudwatchlogs.InputLogEvent{
			Message:   aws.String(event_str),
			Timestamp: aws.Int64(makeMilliTimestamp(logPart["timestamp"].(time.Time))),
		})
	}

	// first request has no SequenceToken - in all subsequent request we set it
	if sequenceToken != "" {
		params.SequenceToken = aws.String(sequenceToken)
	}

	resp, err := svc.PutLogEvents(params)
	if err != nil {
		log.Println(err)
	}
	log.Printf("Pushed %v entries to CloudWatch", len(buffer))

	sequenceToken = *resp.NextSequenceToken
}

func initCloudWatchStream() {
	// service is defined at run time to avoid session expiry in long running processes
	var svc = cloudwatchlogs.New(session.New())
	// set the AWS SDK to use our bundled certs for the minimal container (certs from CoreOS linux)
	svc.Config.HTTPClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}

	_, err := svc.CreateLogStream(&cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  aws.String(logGroupName),
		LogStreamName: aws.String(streamName.String()),
	})

	if err != nil {
		log.Fatal(err)
	}

	log.Println("Created CloudWatch Logs stream:", streamName)
}

func makeMilliTimestamp(input time.Time) int64 {
	return input.UnixNano() / int64(time.Millisecond)
}
