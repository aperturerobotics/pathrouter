// Copyright 2023 Christian Stewart <christian@aperture.us>
// Copyright 2013 Julien Schmidt. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package pathrouter

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/pkg/errors"
)

func TestParams(t *testing.T) {
	ps := Params{
		Param{"param1", "value1"},
		Param{"param2", "value2"},
		Param{"param3", "value3"},
	}
	for i := range ps {
		if val := ps.ByName(ps[i].Key); val != ps[i].Value {
			t.Errorf("Wrong value for %s: Got %s; Want %s", ps[i].Key, val, ps[i].Value)
		}
	}
	if val := ps.ByName("noKey"); val != "" {
		t.Errorf("Expected empty string for not found key; got: %s", val)
	}
}

func TestRouter(t *testing.T) {
	router := New[struct{}]()

	routed := false
	ctx := context.Background()
	router.AddHandler("/user/:name", func(ctx context.Context, reqPath string, p Params, rw struct{}) (bool, error) {
		routed = true
		want := Params{Param{"name", "gopher"}}
		if !reflect.DeepEqual(p, want) {
			return false, errors.Errorf("wrong wildcard values: want %v, got %v", want, p)
		}
		return true, nil
	})

	found, err := router.Serve(ctx, "/user/gopher", struct{}{})
	if err != nil {
		t.Fatal(err.Error())
	}
	if !found || !routed {
		t.Fatal("routing failed")
	}
}

// buildHandler builds a new handler.
func buildHandler[W any](handled *atomic.Bool) Handle[W] {
	return func(ctx context.Context, reqPath string, p Params, w W) (bool, error) {
		if handled == nil {
			return false, nil
		}
		handled.Store(true)
		return true, nil
	}
}

func TestRouterAPI(t *testing.T) {
	var handled atomic.Bool

	router := New[struct{}]()
	router.AddHandler("/Handler", buildHandler[struct{}](&handled))

	ctx := context.Background()
	found, err := router.Serve(ctx, "/Handler", struct{}{})
	if err != nil {
		t.Fatal(err.Error())
	}
	if !found || !handled.Load() {
		t.Error("routing Handler failed")
	}
}

func TestRouterNotFound(t *testing.T) {
	var lastPath string
	handlerFunc := func(ctx context.Context, reqPath string, p Params, rw struct{}) (bool, error) {
		lastPath = reqPath
		return true, nil
	}

	router := New[struct{}]()
	router.AddHandler("/path", handlerFunc)
	router.AddHandler("/dir/", handlerFunc)
	router.AddHandler("/", handlerFunc)

	testRoutes := []struct {
		route    string
		found    bool
		location string
	}{
		{"/path/", true, "/path"},   // TSR -/
		{"/dir", true, "/dir/"},     // TSR +/
		{"", true, "/"},             // TSR +/
		{"/PATH", true, "/path"},    // Fixed Case
		{"/DIR/", true, "/dir/"},    // Fixed Case
		{"/PATH/", true, "/path"},   // Fixed Case -/
		{"/DIR", true, "/dir/"},     // Fixed Case +/
		{"/../path", true, "/path"}, // CleanPath
		{"/nope", false, ""},        // NotFound
	}
	ctx := context.Background()
	for _, tr := range testRoutes {
		lastPath = ""
		found, err := router.Serve(ctx, tr.route, struct{}{})
		if err != nil {
			t.Fatal(err.Error())
		}
		if found != tr.found {
			t.Errorf("NotFound handling route %s failed: expected found=%v but got found=%v", tr.route, tr.found, found)
		}
		if lastPath != tr.location {
			t.Errorf("NotFound handling route %s failed: expected path=%v but got path=%v", tr.route, tr.location, lastPath)
		}
	}
}

func TestRouterNotFoundHandler(t *testing.T) {
	handlerFunc := func(ctx context.Context, reqPath string, p Params, rw struct{}) (bool, error) {
		return true, nil
	}

	routerConf := DefaultConfig[struct{}]()
	var notFoundCalled bool
	routerConf.NotFound = func(ctx context.Context, reqPath string, p Params, rw struct{}) (bool, error) {
		notFoundCalled = true
		return true, nil
	}

	router := NewWithConfig(routerConf)
	router.AddHandler("/path", handlerFunc)

	// Test custom not found handler
	ctx := context.Background()
	wasFound, err := router.Serve(ctx, "/nope", struct{}{})
	if err != nil {
		t.Fatal(err.Error())
	}
	if !notFoundCalled || !wasFound {
		t.Fail()
	}
}

func TestRouterPanicHandler(t *testing.T) {
	routerConf := DefaultConfig[struct{}]()
	var panicHandled bool
	routerConf.PanicHandler = func(ctx context.Context, reqPath string, rw struct{}, panicErr interface{}) {
		panicHandled = true
	}
	router := NewWithConfig(routerConf)

	router.AddHandler("/user/:name", func(ctx context.Context, reqPath string, p Params, rw struct{}) (bool, error) {
		panic("oops!")
	})

	defer func() {
		if rcv := recover(); rcv != nil {
			t.Fatal("handling panic failed")
		}
	}()

	ctx := context.Background()
	_, _ = router.Serve(ctx, "/user/gopher", struct{}{})

	if !panicHandled {
		t.Fatal("panic handler was not called")
	}
}

func TestRouterLookup(t *testing.T) {
	wantParams := Params{Param{"name", "gopher"}}

	router := New[struct{}]()

	// try empty router first
	handle, _, tsr := router.LookupPath("/nope")
	if handle != nil {
		t.Fatalf("Got handle for unregistered pattern: %v", handle)
	}
	if tsr {
		t.Error("Got wrong TSR recommendation!")
	}

	// insert route and try again
	var handled atomic.Bool
	handler := buildHandler[struct{}](&handled)
	router.AddHandler("/user/:name", handler)
	handle, params, _ := router.LookupPath("/user/gopher")
	if handle == nil {
		t.Fatal("Got no handle!")
	} else {
		routed, _ := handle(context.Background(), "/user/gopher", nil, struct{}{})
		if !routed || !handled.Load() {
			t.Fatal("Routing failed!")
		}
	}
	if !reflect.DeepEqual(params, wantParams) {
		t.Fatalf("Wrong parameter values: want %v, got %v", wantParams, params)
	}
	handled.Store(false)

	// route without param
	router.AddHandler("/user", handler)
	handle, params, _ = router.LookupPath("/user")
	if handle == nil {
		t.Fatal("Got no handle!")
	} else {
		routed, _ := handle(context.Background(), "/user", nil, struct{}{})
		if !routed || !handled.Load() {
			t.Fatal("Routing failed!")
		}
	}
	if params != nil {
		t.Fatalf("Wrong parameter values: want %v, got %v", nil, params)
	}

	handle, _, tsr = router.LookupPath("/user/gopher/")
	if handle != nil {
		t.Fatalf("Got handle for unregistered pattern: %v", handle)
	}
	if !tsr {
		t.Error("Got no TSR recommendation!")
	}

	handle, _, tsr = router.LookupPath("/nope")
	if handle != nil {
		t.Fatalf("Got handle for unregistered pattern: %v", handle)
	}
	if tsr {
		t.Error("Got wrong TSR recommendation!")
	}
}
