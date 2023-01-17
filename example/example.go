package main

import (
	"context"
	"fmt"

	"github.com/aperturerobotics/pathrouter"
)

// Responder is a custom struct used in place of a response writer.
type Responder struct {
}

func (r *Responder) Respond(ctx context.Context, path, resp string) error {
	fmt.Printf("path=%s responded with: %s", path, resp)
	return nil
}

func Index(ctx context.Context, reqPath string, p pathrouter.Params, rw *Responder) (bool, error) {
	return true, rw.Respond(ctx, reqPath, "Welcome!\n")
}

func Hello(ctx context.Context, reqPath string, p pathrouter.Params, rw *Responder) (bool, error) {
	return true, rw.Respond(ctx, reqPath, fmt.Sprintf("hello, %s!\n", p.ByName("name")))
}

func main() {
	router := pathrouter.New[*Responder]()
	router.AddHandler("/", Index)
	router.AddHandler("/hello/:name", Hello)

	ctx := context.Background()
	resp := &Responder{}
	router.Serve(ctx, "/", resp)
	router.Serve(ctx, "/hello/world", resp)
	router.Serve(ctx, "/hello/reader", resp)
}
