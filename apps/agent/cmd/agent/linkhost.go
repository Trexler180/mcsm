package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// linkHostForTLS picks the host the helper mod should dial when the agent is
// serving TLS.
//
// The mod validates the agent's certificate against the JVM truststore like any
// other HTTPS client, and hostname verification is part of that. Advertising a
// loopback literal is right for plaintext — it keeps the launch token off the
// network entirely — but a certificate issued for the node's FQDN does not cover
// 127.0.0.1, so the mod rejects it, retries, and never links. The failure is
// silent from the panel's side and looks exactly like a mod that won't start.
//
// So the advertised host has to be one the certificate actually covers. The
// order below prefers staying on loopback and only leaves it when the
// certificate leaves us no choice:
//
//  1. preferred, if the certificate covers it — the common case for a cert with
//     an IP SAN of 127.0.0.1, and for a non-loopback bind whose cert names it.
//  2. "localhost", if covered — still loopback, still off the network.
//  3. the first DNS name in the certificate — verifies, but the dial may now
//     leave the machine, so the caller is expected to say so out loud.
//
// A returned reason is always non-empty when the host differs from preferred, or
// when no usable host was found (host ""), so the caller can log why.
func linkHostForTLS(certFile, preferred string) (host string, reason string) {
	leaf, err := loadLeafCertificate(certFile)
	if err != nil {
		// Do not fall back to the preferred host silently. If we cannot read the
		// certificate we cannot promise the mod will accept it, and a link that
		// retries forever is worse than one that reports why it is off.
		return preferred, fmt.Sprintf("could not read %s (%v); advertising %s unverified", certFile, err, preferred)
	}

	if leaf.VerifyHostname(preferred) == nil {
		return preferred, ""
	}
	if leaf.VerifyHostname("localhost") == nil {
		return "localhost", fmt.Sprintf("certificate does not cover %s; using localhost", preferred)
	}
	for _, name := range leaf.DNSNames {
		if leaf.VerifyHostname(name) == nil {
			return name, fmt.Sprintf(
				"certificate covers neither %s nor localhost; using %s, so helper-mod traffic may leave the loopback interface",
				preferred, name)
		}
	}

	return "", fmt.Sprintf(
		"certificate covers no host this agent can be reached at (SANs: %v, IPs: %v); helper mod link disabled",
		leaf.DNSNames, leaf.IPAddresses)
}

// loadLeafCertificate reads the first certificate from a PEM bundle, which is
// the leaf by convention — intermediates follow it.
func loadLeafCertificate(certFile string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	for block, rest := pem.Decode(raw); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			continue
		}
		return x509.ParseCertificate(block.Bytes)
	}
	return nil, fmt.Errorf("no CERTIFICATE block found")
}
