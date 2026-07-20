package main

import "fmt"

type dashboardListenerKind string

const (
	dashboardHTTPListener    dashboardListenerKind = "http"
	dashboardHTTPSListener   dashboardListenerKind = "https"
	dashboardReceiptListener dashboardListenerKind = "receipt"
)

func dashboardListenerAddress(host string, port uint16) string {
	return fmt.Sprintf("%s:%d", host, port)
}
