package main
import (
  "context"; "fmt"; "time"
  discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
  "github.com/Muxcore-Media/core/sdk/go/client"
)
func main() {
  c,_ := client.Dial("127.0.0.1:9090", client.WithInsecure())
  defer c.Close()
  ctx,_ := context.WithTimeout(context.Background(), 10*time.Second)
  resp, err := c.Discovery.Raw().ListAll(ctx, &discoveryv1.ListAllRequest{})
  fmt.Println("ListAll", err, "n", len(resp.GetEntries()))
  for i,e := range resp.GetEntries() {
    if i>=8 {break}
    fmt.Println(" ", e.GetInfo().GetId(), e.GetState())
  }
}
