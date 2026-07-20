// Debug helper: dump the first N raw alert entities as prototext.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"google.golang.org/protobuf/encoding/prototext"

	"transit-monitor/internal/gtfsrt"
)

func main() {
	n := flag.Int("n", 2, "number of entities to dump")
	flag.Parse()

	msg, err := gtfsrt.NewFetcher(gtfsrt.DefaultAlertsURL).Fetch(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for i, e := range msg.GetEntity() {
		if i >= *n {
			break
		}
		fmt.Println(prototext.Format(e))
		fmt.Println("----")
	}
}
