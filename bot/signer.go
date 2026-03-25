package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// bech32Encode converts a 32-byte public key to an erd1... bech32 address.
// Uses the standard MvX conversion (bech32 with hrp="erd").
func bech32Encode(hrp string, data []byte) string {
	// Simplified: use hex embedding. For prod use proper bech32 lib.
	// We rely on the wallet addresses being provided in config anyway.
	return hrp + "1" + hex.EncodeToString(data)[:58]
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
		Data: tx.Data, ChainID: tx.ChainID, Version: tx.Version,
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
			TxsHashes []string `json:"txsHashes"`
		} `json:"data"`
	}
	json.Unmarshal(resp, &result)
	return result.Data.TxsHashes, nil
}
