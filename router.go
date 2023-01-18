// Copyright 2023 Christian Stewart <christian@aperture.us>
// Copyright 2013 Julien Schmidt. All rights reserved.
//
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

// Package pathrouter is a trie based high performance path request router.
//
// The router matches incoming requests by the request path.
// If a handle is registered for the path, it is called with the path params.
//
// The registered path, against which the router matches incoming requests, can
// contain two types of parameters:
//
//	Syntax    Type
//	:name     named parameter
//	*name     catch-all parameter
//
// Named parameters are dynamic path segments. They match anything until the
// next '/' or the path end:
//
//	Path: /blog/:category/:post
//
//	Requests:
//	 /blog/go/request-routers            match: category="go", post="request-routers"
//	 /blog/go/request-routers/           no match, but the router would redirect
//	 /blog/go/                           no match
//	 /blog/go/request-routers/comments   no match
//
// Catch-all parameters match anything until the path end, including the
// directory index (the '/' before the catch-all). Since they match anything
// until the end, catch-all parameters must always be the final path element.
//
//	Path: /files/*filepath
//
//	Requests:
//	 /files/                             match: filepath="/"
//	 /files/LICENSE                      match: filepath="/LICENSE"
//	 /files/templates/article.html       match: filepath="/templates/article.html"
//	 /files                              no match, but the router would redirect
//
// The value of parameters is saved as a slice of the Param struct, consisting
// each of a key and a value. The slice is passed to the Handle func as a third
// parameter.
// There are two ways to retrieve the value of a parameter:
//
//	// by the name of the parameter
//	user := ps.ByName("user") // defined by :user or *user
//
//	// by the index of the parameter. This way you can also get the name (key)
//	thirdKey   := ps[2].Key   // the name of the 3rd parameter
//	thirdValue := ps[2].Value // the value of the 3rd parameter
package pathrouter

import (
	"context"
	"sync"
)

// Handle is a function that can be registered to a route to handle requests.
// Returns if the request was handled and any error.
// If false, nil is returned, assumes the function has returned "not found."
type Handle[W any] func(ctx context.Context, reqPath string, p Params, rw W) (bool, error)

// Param is a single URL parameter, consisting of a key and a value.
type Param struct {
	Key   string
	Value string
}

// Params is a Param-slice, as returned by the router.
// The slice is ordered, the first URL parameter is also the first slice value.
// It is therefore safe to read values by the index.
type Params []Param

// ByName returns the value of the first Param which key matches the given name.
// If no matching Param is found, an empty string is returned.
func (ps Params) ByName(name string) string {
	for _, p := range ps {
		if p.Key == name {
			return p.Value
		}
	}
	return ""
}

// RouterConfig are optional configuration parameters for the Router.
type RouterConfig[W any] struct {
	// Enables automatic redirection if the current route can't be matched but a
	// handler for the path with (without) the trailing slash exists.
	// For example if /foo/ is requested but a route only exists for /foo, the
	// client is redirected to /foo
	RedirectTrailingSlash bool

	// RedirectFixedPath configures the router to fix the current request path, if no
	// handle is registered for it.
	// First superfluous path elements like ../ or // are removed.
	// Afterwards the router does a case-insensitive lookup of the cleaned path.
	// If a handle can be found for this route, the router makes a redirection
	// to the corrected path.
	// For example /FOO and /..//Foo could be redirected to /foo.
	// RedirectTrailingSlash is independent of this option.
	RedirectFixedPath bool

	// NotFound is called when no matching route is found.
	NotFound Handle[W]

	// Function to handle panics recovered from handlers.
	// If nil, no recover() will be called (panics will throw).
	// The fourth parameter is the error from recover().
	PanicHandler func(ctx context.Context, reqPath string, rw W, panicErr interface{})
}

// Router can be used to dispatch URL requests to different handler functions
// via configurable routes. The request and response types are generic.
//
// R is the request type.
// W is the response writer type.
type Router[W any] struct {
	conf       RouterConfig[W]
	tree       *node[W]
	paramsPool sync.Pool
	maxParams  uint16
}

// DefaultConfig returns the default configuration if none is specified.
// Path auto-correction, including trailing slashes, is enabled by default.
func DefaultConfig[W any]() RouterConfig[W] {
	return RouterConfig[W]{
		RedirectTrailingSlash: true,
		RedirectFixedPath:     true,
	}
}

// New returns a new Router with default configuration.
// Path auto-correction, including trailing slashes, is enabled by default.
func New[W any]() *Router[W] {
	return NewWithConfig(DefaultConfig[W]())
}

// NewWithConfig constructs a new Router with the given config.
//
// All configuration values are defaulted to false.
func NewWithConfig[W any](conf RouterConfig[W]) *Router[W] {
	return &Router[W]{
		conf: conf,
	}
}

func (r *Router[W]) getParams() *Params {
	ps, _ := r.paramsPool.Get().(*Params)
	*ps = (*ps)[0:0] // reset slice
	return ps
}

func (r *Router[W]) putParams(ps *Params) {
	if ps != nil {
		r.paramsPool.Put(ps)
	}
}

// AddHandler registers a new request handle with the given path.
func (r *Router[W]) AddHandler(path string, handle Handle[W]) {
	if handle == nil {
		return
	}

	if len(path) == 0 {
		path = "/"
	} else if path[0] != '/' {
		path = "/" + path
	}

	var varsCount uint16
	root := r.tree
	if root == nil {
		root = new(node[W])
		r.tree = root
	}

	root.addRoute(path, handle)

	// Update maxParams
	if paramsCount := countParams(path); paramsCount+varsCount > r.maxParams {
		r.maxParams = paramsCount + varsCount
	}

	// Lazy-init paramsPool alloc func
	if r.paramsPool.New == nil && r.maxParams > 0 {
		r.paramsPool.New = func() interface{} {
			ps := make(Params, 0, r.maxParams)
			return &ps
		}
	}
}

// recoverPanic recovers from a panic while processing a path.
func (r *Router[W]) recoverPanic(ctx context.Context, reqPath string, wr W) {
	defer func() {
		// if the panic handler panics, just give up :)
		_ = recover()
	}()
	if rcv := recover(); rcv != nil && r.conf.PanicHandler != nil {
		r.conf.PanicHandler(ctx, reqPath, wr, rcv)
	}
}

// LookupPath allows the manual lookup of a handler with a path.
// This is e.g. useful to build a framework around this router.
// If the path was found, it returns the handle function and the path parameter
// values. Otherwise the third return value indicates whether a redirection to
// the same path with an extra / without the trailing slash should be performed.
func (r *Router[W]) LookupPath(path string) (Handle[W], Params, bool) {
	if root := r.tree; root != nil {
		handle, ps, tsr := root.getValue(path, r.getParams)
		if handle == nil {
			r.putParams(ps)
			return nil, nil, tsr
		}
		if ps == nil {
			return handle, nil, tsr
		}
		return handle, *ps, tsr
	}
	return nil, nil, false
}

// Serve serves a request with the router.
// Returns if the request was handled and any error.
// Note: if the error handler is set, may return true even if not found.
func (r *Router[W]) Serve(ctx context.Context, reqPath string, wr W) (bool, error) {
	if r.conf.PanicHandler != nil {
		defer r.recoverPanic(ctx, reqPath, wr)
	}

	if reqPath == "" {
		reqPath = "/"
	}

	if root := r.tree; root != nil {
		if handle, ps, tsr := root.getValue(reqPath, r.getParams); handle != nil {
			var params Params
			if ps != nil {
				params = *ps
				defer r.putParams(ps)
			}
			found, handlerErr := handle(ctx, reqPath, params, wr)
			if found || handlerErr != nil {
				return found, handlerErr
			}
		} else if reqPath != "/" && reqPath != "" {
			if tsr && r.conf.RedirectTrailingSlash {
				if len(reqPath) > 1 && reqPath[len(reqPath)-1] == '/' {
					reqPath = reqPath[:len(reqPath)-1]
				} else {
					reqPath = reqPath + "/"
				}
				return r.Serve(ctx, reqPath, wr)
			}

			// Try to fix the request path
			if r.conf.RedirectFixedPath {
				fixedPath, fixedFound := root.findCaseInsensitivePath(
					CleanPath(reqPath),
					r.conf.RedirectTrailingSlash,
				)
				if fixedFound {
					reqPath = fixedPath
					return r.Serve(ctx, reqPath, wr)
				}
			}
		}
	}

	// not found
	if r.conf.NotFound == nil {
		return false, nil
	}

	return r.conf.NotFound(ctx, reqPath, nil, wr)
}
