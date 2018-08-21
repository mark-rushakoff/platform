package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"log"
	"strings"
	"time"

	"github.com/influxdata/platform"
	"github.com/influxdata/platform/gather"
	"github.com/influxdata/platform/nats"
)

const (
	subjectParse = "sub_scraper"
	subjectStore = "sub_store"
)

func main() {
	logger := new(log.Logger)
	orgIDstr := flag.String("orgID", "", "the organization id")
	bucketIDstr := flag.String("bucketID", "", "the bucket id")
	hostStr := flag.String("pHost", "", "the promethus host")
	influxStr := flag.String("influxURLs", "", "comma seperated urls")
	flag.Parse()

	orgID, err := platform.IDFromString(*orgIDstr)
	if err != nil || orgID == nil || orgID.String() == "" {
		log.Fatal("Invalid orgID")
	}

	bucketID, err := platform.IDFromString(*bucketIDstr)
	if err != nil || bucketID == nil || bucketID.String() == "" {
		log.Fatal("Invalid bucketID")
	}

	if *hostStr == "" {
		log.Fatal("Invalid host")
	}
	pURL := *hostStr + "/metrics"

	influxURL := strings.Split(*influxStr, ",")
	if len(influxURL) == 0 {
		influxURL = []string{
			"http://localhost:8086",
		}
	}

	server := nats.NewServer(nats.Config{
		FilestoreDir: ".",
	})

	if err := server.Open(); err != nil {
		log.Fatalf("nats server fatal err %v", err)
	}

	subscriber := nats.NewQueueSubscriber("nats-subscriber")
	if err := subscriber.Open(); err != nil {
		log.Fatalf("nats parse subscriber open issue %v", err)
	}

	publisher := nats.NewAsyncPublisher("nats-publisher")
	if err := publisher.Open(); err != nil {
		log.Fatalf("nats parse publisher open issue %v", err)
	}

	if err := subscriber.Subscribe(subjectParse, "", &scraperHandler{
		Scraper: gather.NewPrometheusScraper(
			gather.NewNatsStorage(
				gather.NewInfluxStorage(influxURL),
				subjectStore,
				logger,
				publisher,
				subscriber,
			),
		),
	}); err != nil {
		log.Fatalf("nats subscribe error")
	}

	msg := scraperRequest{
		HostURL:  pURL,
		OrgID:    *orgID,
		BucketID: *bucketID,
	}

	go scheduleGetMetrics(msg, publisher)

	keepalive := make(chan struct{})
	<-keepalive
}

// scheduleGetMetrics will send the scraperRequest to publisher
// for every 2 second
func scheduleGetMetrics(msg scraperRequest, publisher nats.Publisher) {
	buf := new(bytes.Buffer)
	b, _ := json.Marshal(msg)
	buf.Write(b)
	publisher.Publish(subjectParse, buf)

	time.Sleep(2 * time.Second)
	scheduleGetMetrics(msg, publisher)
}

type scraperRequest struct {
	HostURL  string      `json:"host_url"`
	OrgID    platform.ID `json:"org"`
	BucketID platform.ID `json:"bucket"`
	err      error
}

type scraperHandler struct {
	Scraper gather.Scrapper
}

func (h *scraperHandler) Process(s nats.Subscription, m nats.Message) {
	defer m.Ack()
	msg := new(scraperRequest)
	err := json.Unmarshal(m.Data(), msg)
	if err != nil {
		log.Printf("scrapper processing error %v\n", err)
		return
	}
	err = h.Scraper.Gather(context.Background(), msg.OrgID, msg.BucketID, msg.HostURL)
	if err != nil {
		println(err.Error())
	}
}

func csvParseNGather() {

}
