package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"github.com/diogomonica/actuary/cmd/actuary/check"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

var htmlPath string

func init() {
	ServerCmd.Flags().StringVarP(&htmlPath, "htmlPath", "p", "/cmd/actuary/server/results", "Path to folder that holds html, js, css for browser -- relative to current working directory.")
}

type outputData struct {
	Mu      *sync.Mutex
	Outputs map[string][]byte
}

var (
	ServerCmd = &cobra.Command{
		Use:   "server",
		Short: "Aggregate actuary output for swarm",
		RunE: func(cmd *cobra.Command, args []string) error {
			mux := http.NewServeMux()
			m := make(map[string][]byte)
			var report = outputData{Mu: &sync.Mutex{}, Outputs: m}
			var reqList []check.Request

			// Get list of all nodes in the swarm via Docker API call
			// Used for comparison to see which nodes have yet to be processed
			ctx := context.Background()
			cli, err := client.NewEnvClient()
			if err != nil {
				log.Fatalf("Could not create new client: %s", err)
			}
			nodeList, err := cli.NodeList(ctx, types.NodeListOptions{})
			if err != nil {
				log.Fatalf("Could not get list of nodes: %s", err)
			}

			cfg := &tls.Config{
				MinVersion:               tls.VersionTLS12,
				CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
				PreferServerCipherSuites: true,
				CipherSuites: []uint16{
					tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
					tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_RSA_WITH_AES_256_CBC_SHA,
				},
			}
			srv := &http.Server{
				Addr:         ":8000",
				Handler:      mux,
				TLSConfig:    cfg,
				TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler), 0),
			}

			// Send official list of nodes from docker client to browser
			mux.HandleFunc("/getNodeList", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.Header().Add("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
				w.WriteHeader(http.StatusOK)
				var b bytes.Buffer
				for _, node := range nodeList {
					b.Write([]byte(node.ID + " "))
				}
				b.WriteTo(w)
			})

			// Where nodes send DATA from check.go, where javascript requests receives specific node DATA
			mux.HandleFunc("/results", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
				if r.Method == "POST" {
					output, err := ioutil.ReadAll(r.Body)
					if err != nil {
						log.Fatalf("Error reading: %s", err)
					}
					var req check.Request
					err = json.Unmarshal(output, &req)
					if err != nil {
						log.Fatalf("Error unmarshalling id: %s", err)
					}
					reqList = append(reqList, req)
					nodeID := string(req.NodeID)
					results := req.Results
					report.Mu.Lock()
					report.Outputs[nodeID] = results
					report.Mu.Unlock()
				} else if r.Method == "GET" {
					nodeID := r.URL.Query().Get("nodeID")
					check := r.URL.Query().Get("check")
					if nodeID != "" {
						//Determine whether or not a specified node has been processed -- ie if its results are ready to be displayed
						if check == "true" {
							w.Header().Set("Content-Type", "text/html")
							found := false
							for _, req := range reqList {
								if string(req.NodeID) == string(nodeID) {
									w.Write([]byte("true"))
									found = true
								}
							}
							if !found {
								w.Write([]byte("false"))
							}
						} else {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusOK)
							report.Mu.Lock()
							w.Write(report.Outputs[nodeID])
							report.Mu.Unlock()
						}
					} else {
						log.Fatalf("Node ID not entered")
					}
				}
			})

			// Path to return css/js/html
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				currentDir, err := os.Getwd()
				if err != nil {
					log.Fatalf("Error getting current directory: %s", err)
				}
				path := filepath.Join(currentDir, htmlPath)
				w.Header().Add("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
				handler := http.FileServer(http.Dir(path))
				handler.ServeHTTP(w, r)
			})

			err = srv.ListenAndServeTLS(os.Getenv("TLS_CERT"), os.Getenv("TLS_KEY"))
			if err != nil {
				log.Fatalf("ListenAndServeTLS: %s", err)
			}
			return nil
		},
	}
)
