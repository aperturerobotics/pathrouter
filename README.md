# Path Router

[![GoDoc Widget]][GoDoc] [![Go Report Card Widget]][Go Report Card]

> Fork of julienschmidt/httprouter to route paths without HTTP semantics.

[GoDoc]: https://godoc.org/github.com/aperturerobotics/httprouter
[GoDoc Widget]: https://godoc.org/github.com/aperturerobotics/httprouter?status.svg
[Go Report Card Widget]: https://goreportcard.com/badge/github.com/aperturerobotics/httprouter
[Go Report Card]: https://goreportcard.com/report/github.com/aperturerobotics/httprouter

## Introduction

**pathrouter** is a lightweight high performance path request router.

In contrast to the [default mux](https://golang.org/pkg/net/http/#ServeMux) of
Go's `net/http** package, this router supports variables in the routing pattern.
**It does not use HTTP semantics and is intended for matching paths only.***

The router is optimized for high performance and a small memory footprint. It
scales well even with very long paths and a large number of routes. A
compressing dynamic trie (radix tree) structure is used for efficient matching.

## Features

**Only explicit matches:** With other routers, like [`http.ServeMux`](https://golang.org/pkg/net/http/#ServeMux), a requested URL path could match multiple patterns. Therefore they have some awkward pattern priority rules, like *longest match* or *first registered, first matched*. By design of this router, a request can only match exactly one or no route. As a result, there are also no unintended matches, which makes it great for SEO and improves the user experience.

**Stop caring about trailing slashes:** Choose the URL style you like, the router automatically redirects the client if a trailing slash is missing or if there is one extra. Of course it only does so, if the new path has a handler. If you don't like it, you can [turn off this behavior](https://godoc.org/github.com/aperturerobotics/pathrouter#Router.RedirectTrailingSlash).

**Path auto-correction:** Besides detecting the missing or additional trailing slash at no extra cost, the router can also fix wrong cases and remove superfluous path elements (like `../` or `//`). Is [CAPTAIN CAPS LOCK](http://www.urbandictionary.com/define.php?term=Captain+Caps+Lock) one of your users? HttpRouter can help him by making a case-insensitive look-up and redirecting him to the correct URL.

**Parameters in your routing pattern:** Stop parsing the requested URL path, just give the path segment a name and the router delivers the dynamic value to you. Because of the design of the router, path parameters are very cheap.

**Zero Garbage:** The matching and dispatching process generates zero bytes of garbage. The only heap allocations that are made are building the slice of the key-value pairs for path parameters, and building new context and request objects (the latter only in the standard `Handler`/`HandlerFunc` API). In the 3-argument API, if the request path contains no parameters not a single heap allocation is necessary.

**Best Performance:** [Benchmarks speak for themselves](https://github.com/julienschmidt/go-http-routing-benchmark). See below for technical details of the implementation.

**No more server crashes:** You can set a [Panic handler](https://godoc.org/github.com/aperturerobotics/pathrouter#Router.PanicHandler) to deal with panics. The router then recovers and lets the `PanicHandler` log what happened.

## Usage

This is just a quick introduction, view the [Docs](http://pkg.go.dev/github.com/aperturerobotics/pathrouter) for details.

Let's start with a trivial example:

```go
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

    // path=/ responded with: Welcome!
	router.Serve(ctx, "/", resp)
    // path=/hello/world responded with: hello, world!
	router.Serve(ctx, "/hello/world", resp)
    // path=/hello/reader responded with: hello, reader!
	router.Serve(ctx, "/hello/reader", resp)
}
```

Some things to note when compared to typical HTTP request handlers:

 - The router uses generics to pass a custom response writer type.
 - The context is passed separately in the arguments to handlers.
 - All handlers can return an error and a bool indicating "not found" if false.
 - The router strictly operates on paths and ignores the rest of the URL.

### Named parameters

As you can see, `:name` is a *named parameter*. The values are accessible via `pathrouter.Params`, which is just a slice of `pathrouter.Param`s. You can get the value of a parameter either by its index in the slice, or by using the `ByName(name)` method: `:name` can be retrieved by `ByName("name")`.

When using a `http.Handler` (using `router.Handler` or `http.HandlerFunc`) instead of HttpRouter's handle API using a 3rd function parameter, the named parameters are stored in the `request.Context`. See more below under [Why doesn't this work with http.Handler?](#why-doesnt-this-work-with-httphandler).

Named parameters only match a single path segment:

```
Pattern: /user/:user

 /user/gordon              match
 /user/you                 match
 /user/gordon/profile      no match
 /user/                    no match
```

**Note:** Since this router has only explicit matches, you can not register static routes and parameters for the same path segment. For example you can not register the patterns `/user/new` and `/user/:user` for the same request method at the same time. The routing of different request methods is independent from each other.

### Catch-All parameters

The second type are *catch-all* parameters and have the form `*name`. Like the name suggests, they match everything. Therefore they must always be at the **end** of the pattern:

```
Pattern: /src/*filepath

 /src/                     match
 /src/somefile.go          match
 /src/subdir/somefile.go   match
```

## How does it work?

The router relies on a tree structure which makes heavy use of *common prefixes*, it is basically a *compact* [*prefix tree*](https://en.wikipedia.org/wiki/Trie) (or just [*Radix tree*](https://en.wikipedia.org/wiki/Radix_tree)). Nodes with a common prefix also share a common parent. Here is a short example what the routing tree for the `GET` request method could look like:

```
Priority   Path             Handle
9          \                *<1>
3          ├s               nil
2          |├earch\         *<2>
1          |└upport\        *<3>
2          ├blog\           *<4>
1          |    └:post      nil
1          |         └\     *<5>
2          ├about-us\       *<6>
1          |        └team\  *<7>
1          └contact\        *<8>
```

Every `*<num>` represents the memory address of a handler function (a pointer). If you follow a path trough the tree from the root to the leaf, you get the complete route path, e.g `\blog\:post\`, where `:post` is just a placeholder ([*parameter*](#named-parameters)) for an actual post name. Unlike hash-maps, a tree structure also allows us to use dynamic parts like the `:post` parameter, since we actually match against the routing patterns instead of just comparing hashes. [As benchmarks show](https://github.com/julienschmidt/go-http-routing-benchmark), this works very well and efficient.

Since URL paths have a hierarchical structure and make use only of a limited set of characters (byte values), it is very likely that there are a lot of common prefixes. This allows us to easily reduce the routing into ever smaller problems. Moreover the router manages a separate tree for every request method. For one thing it is more space efficient than holding a method->handle map in every single node, it also allows us to greatly reduce the routing problem before even starting the look-up in the prefix-tree.

For even better scalability, the child nodes on each tree level are ordered by priority, where the priority is just the number of handles registered in sub nodes (children, grandchildren, and so on..). This helps in two ways:

1. Nodes which are part of the most routing paths are evaluated first. This helps to make as much routes as possible to be reachable as fast as possible.
2. It is some sort of cost compensation. The longest reachable path (highest cost) can always be evaluated first. The following scheme visualizes the tree structure. Nodes are evaluated from top to bottom and from left to right.

```
├------------
├---------
├-----
├----
├--
├--
└-
```

## Why doesn't this work with `http.Handler`?

This router is intended to be used for situations where routing paths are the
primary concern. It's not intended to be used as a HTTP handler, although it
definitely could be, just pass http.ResponseWriter as the response type.

## Attribution

This repository is a fork of [julienschmidt/httprouter].

[julienschmidt/httprouter]: https://github.com/julienschmidt/httprouter
