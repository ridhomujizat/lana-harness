// Package openaiproxy serves the router through an OpenAI-compatible HTTP API
// (REQ-RTR-015, API-RTR-015, API-RTR-016): POST {prefix}/chat/completions
// (streaming and non-streaming, tools, images, reasoning_effort) and GET
// {prefix}/models. The request "model" is a combo name or a "provider/model"
// address, so proxy calls get the router's retry, fallback, cooldown, and call
// records (source "proxy").
//
// The handler routes on the path left after its prefix. Mount it either with
// http.StripPrefix or with WithPrefix, not both. Lana's web server mounts it at
// /router/v1/ like this:
//
//	mux.Handle("/router/v1/", http.StripPrefix("/router/v1", openaiproxy.NewHandler(rt, keys.Check)))
//
// which serves POST /router/v1/chat/completions and GET /router/v1/models. The
// same handler with openaiproxy.WithPrefix("/router/v1") can be registered on
// the mux directly instead.
//
// Every request needs "Authorization: Bearer <key>" (or "x-api-key: <key>").
// The auth function decides whether a key is valid and returns its ID; the
// handler never calls upstream for a request it rejects. Each call record
// carries Agent "proxy:<keyID>" so the request log shows which key made the
// call, and TurnID set to the response ID ("chatcmpl-…") so all attempts of
// one proxy request can be grouped.
//
// Another Go service mounts the proxy like this:
//
//	package main
//
//	import (
//		"log"
//		"net/http"
//		"os"
//
//		"github.com/ridhomujizat/lana-harness/core"
//		_ "github.com/ridhomujizat/lana-harness/providers/openai" // registers the openai, deepseek, openrouter, openai_compat kinds
//		"github.com/ridhomujizat/lana-harness/router"
//		"github.com/ridhomujizat/lana-harness/router/openaiproxy"
//	)
//
//	func main() {
//		rt, err := router.New(router.Config{
//			Providers: map[string]core.ProviderConfig{
//				"deepseek": {Kind: "deepseek", BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY"},
//			},
//			Combos: map[string]router.Combo{
//				"coding": {Models: []string{"deepseek/deepseek-flash"}},
//			},
//		}, router.WithSecrets(mySecrets))
//		if err != nil {
//			log.Fatal(err)
//		}
//		defer rt.Close()
//		want := os.Getenv("PROXY_KEY")
//		auth := func(bearer string) (string, bool) { return "default", bearer != "" && bearer == want }
//		mux := http.NewServeMux()
//		mux.Handle("/router/v1/", http.StripPrefix("/router/v1", openaiproxy.NewHandler(rt, auth)))
//		log.Fatal(http.ListenAndServe("127.0.0.1:8080", mux))
//	}
//
// where mySecrets is any core.Secrets (for example one that reads the
// environment). An OpenAI client then uses base URL
// http://127.0.0.1:8080/router/v1 and model "coding".
package openaiproxy
