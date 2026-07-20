//go:build !agentcompat

package main

import (
	"net"
	"net/http"
)

func openReceiptGateListener() (net.Listener, error) { return nil, nil }

func openDashboardListener(network, address string, _ dashboardListenerKind) (net.Listener, error) {
	return net.Listen(network, address)
}

func serveDashboardHTTPS(server *http.Server, certificatePath, keyPath string) error {
	return server.ListenAndServeTLS(certificatePath, keyPath)
}
