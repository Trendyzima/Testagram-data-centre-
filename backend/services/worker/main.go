package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr:=os.Getenv("WORKER_ADDR"); if addr=="" { addr=":8091" }
	http.HandleFunc("/healthz", func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusOK)})
	log.Printf("Testagram local worker listening on %s",addr)
	log.Fatal(http.ListenAndServe(addr,nil))
}
