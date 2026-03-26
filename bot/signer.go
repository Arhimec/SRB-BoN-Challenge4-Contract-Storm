package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// parsePEM extracts the ED25519 private key from a MultiversX PEM file.
// Returns (privKey, walletAddress, error).
func parsePEM(path string) (ed25519.PrivateKey, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	lines := strings.Split(string(data), "\n")
	var b64 strings.Builder
	inBlock := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-----BEGIN") {
			inBlock = true
			continue
		}
		if strings.HasPrefix(line, "-----END") {
			break
		}
		if inBlock {
			b64.WriteString(line)
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		return nil, "", fmt.Errorf("base64 decode: %w", err)
	}
	// decoded bytes are the hex-encoded seed (64 hex chars = 32 seed bytes)
	hexStr := string(decoded)
	seedHex := hexStr
	if len(hexStr) >= 128 {
		seedHex = hexStr[:64] // first 32 bytes = private seed
	}
	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		return nil, "", fmt.Errorf("hex decode seed: %w", err)
	}
	privKey := ed25519.NewKeyFromSeed(seed)
	pubKey := privKey.Public().(ed25519.PublicKey)
	addr := bech32Encode("erd", pubKey)
	return privKey, addr, nil
}

// bech32Encode converts a 32-byte public key to a valid bech32 erd1 address.
func bech32Encode(hrp string, data []byte) string {
	const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

	// convertbits: 8→5
	conv := func(d []byte) []byte {
		var out []byte
		acc, bits := 0, 0
		for _, v := range d {
			acc = (acc << 8) | int(v)
			bits += 8
			for bits >= 5 {
				bits -= 5
				out = append(out, byte((acc>>bits)&31))
			}
		}
		if bits > 0 {
			out = append(out, byte((acc<<(5-bits))&31))
		}
		return out
	}

	polymod := func(values []byte) uint32 {
		c := uint32(1)
		gen := []uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
		for _, v := range values {
			b := c >> 25
			c = (c&0x1ffffff)<<5 ^ uint32(v)
			for i, g := range gen {
				if (b>>uint(i))&1 == 1 {
					c ^= g
				}
			}
		}
		return c
	}

	hrpExpand := func(h string) []byte {
		var r []byte
		for _, c := range h {
			r = append(r, byte(c>>5))
		}
		r = append(r, 0)
		for _, c := range h {
			r = append(r, byte(c&31))
		}
		return r
	}

	words := conv(data)
	enc := append(hrpExpand(hrp), words...)
	enc = append(enc, 0, 0, 0, 0, 0, 0)
	pm := polymod(enc) ^ 1
	cs := make([]byte, 6)
	for i := range cs {
		cs[i] = byte((pm >> (5 * (5 - i))) & 31)
	}
	combined := append(words, cs...)
	out := []byte(hrp + "1")
	for _, b := range combined {
		out = append(out, charset[b])
	}
	return string(out)
}

// Transaction represents a signed MultiversX transaction (proxy wire format).
// Data MUST be base64-encoded — the proxy decodes it internally.
type Transaction struct {
	Nonce     uint64 `json:"nonce"`
	Value     string `json:"value"`
	Receiver  string `json:"receiver"`
	Sender    string `json:"sender"`
	GasPrice  uint64 `json:"gasPrice"`
	GasLimit  uint64 `json:"gasLimit"`
	Data      string `json:"data,omitempty"` // base64-encoded transaction data
	ChainID   string `json:"chainID"`
	Version   int    `json:"version"`
	Signature string `json:"signature,omitempty"`
}

// NewTx creates a Transaction with data correctly base64-encoded.
func NewTx(nonce uint64, sender, receiver, value string, gasPrice, gasLimit uint64, rawData, chainID string, version int) *Transaction {
	dataB64 := ""
	if rawData != "" {
		dataB64 = base64.StdEncoding.EncodeToString([]byte(rawData))
	}
	return &Transaction{
		Nonce: nonce, Value: value, Receiver: receiver, Sender: sender,
		GasPrice: gasPrice, GasLimit: gasLimit, Data: dataB64,
		ChainID: chainID, Version: version,
	}
}

// sign serializes the tx (without signature) and ED25519-signs it.
func sign(tx *Transaction, privKey ed25519.PrivateKey) error {
	type txForSigning struct {
		Nonce    uint64 `json:"nonce"`
		Value    string `json:"value"`
		Receiver string `json:"receiver"`
		Sender   string `json:"sender"`
		GasPrice uint64 `json:"gasPrice"`
		GasLimit uint64 `json:"gasLimit"`
		Data     string `json:"data,omitempty"`
		ChainID  string `json:"chainID"`
		Version  int    `json:"version"`
	}
	toSign := txForSigning{
		Nonce: tx.Nonce, Value: tx.Value, Receiver: tx.Receiver,
		Sender: tx.Sender, GasPrice: tx.GasPrice, GasLimit: tx.GasLimit,
		Data: tx.Data, ChainID: tx.ChainID, Version: 2,
	}
	serialized, err := json.Marshal(toSign)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(privKey, serialized)
	tx.Signature = hex.EncodeToString(sig)
	return nil
}

// httpPost sends a JSON POST and returns the response body.
func httpPost(url string, body []byte) ([]byte, error) {
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// broadcast sends a signed transaction to the proxy and returns the txHash.
func broadcast(proxyURL string, tx *Transaction, privKey ed25519.PrivateKey) (string, error) {
	if err := sign(tx, privKey); err != nil {
		return "", err
	}
	body, err := json.Marshal(tx)
	if err != nil {
		return "", err
	}
	resp, err := httpPost(proxyURL+"/transaction/send", body)
	if err != nil {
		return "", err
	}
	var result struct {
		Data  struct{ TxHash string `json:"txHash"` } `json:"data"`
		Error string                                   `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("proxy: %s", result.Error)
	}
	return result.Data.TxHash, nil
}

// bulkBroadcast sends multiple signed transactions in a single request.
func bulkBroadcast(proxyURL string, txs []*Transaction, privKey ed25519.PrivateKey) ([]string, error) {
	for _, tx := range txs {
		tx.Version = 2
		if err := sign(tx, privKey); err != nil {
			return nil, err
		}
	}
	body, _ := json.Marshal(txs)
	resp, err := httpPost(proxyURL+"/transaction/send-multiple", body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data struct {
			TxsHashes map[string]string `json:"txsHashes"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		log.Printf("Proxy Raw Response: %s", string(resp))
		return nil, fmt.Errorf("unmarshal error: %v", err)
	}
	if result.Error != "" {
		log.Printf("Proxy Error Response: %s", string(resp))
		return nil, fmt.Errorf("proxy: %s", result.Error)
	}
	
	// send-multiple actually returns a map of index -> hash or something similar depending on API version
	// Let's just create a list of hashes. Sometimes it's a map. Let's try map or just return body if empty
	if len(result.Data.TxsHashes) == 0 {
		var fallbackResult struct {
			Data struct {
				NumOfTxs int `json:"numOfTxs"`
				TxsHashes map[string]string `json:"txsHashes"`
			} `json:"data"`
			Error string `json:"error"`
		}
		json.Unmarshal(resp, &fallbackResult)
		if fallbackResult.Error != "" {
			return nil, fmt.Errorf("proxy: %s", fallbackResult.Error)
		}
		if fallbackResult.Data.NumOfTxs == 0 {
			return nil, fmt.Errorf("proxy accepted 0 txs: %s", string(resp))
		}
		hashes := make([]string, 0, len(txs))
		for _, h := range fallbackResult.Data.TxsHashes {
			hashes = append(hashes, h)
		}
		return hashes, nil
	}
	
	hashes := make([]string, 0, len(result.Data.TxsHashes))
	for _, h := range result.Data.TxsHashes {
		hashes = append(hashes, h)
	}
	return hashes, nil
}
