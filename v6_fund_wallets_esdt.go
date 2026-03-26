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
	"regexp"
	"strings"
	"time"
)

type Transaction struct {
	Nonce     uint64 `json:"nonce"`
	Value     string `json:"value"`
	Receiver  string `json:"receiver"`
	Sender    string `json:"sender"`
	GasPrice  uint64 `json:"gasPrice"`
	GasLimit  uint64 `json:"gasLimit"`
	Data      string `json:"data,omitempty"`
	ChainID   string `json:"chainID"`
	Version   int    `json:"version"`
	Signature string `json:"signature,omitempty"`
}

func parsePEM(file string) (ed25519.PrivateKey, string, error) {
	b, _ := os.ReadFile(file)
	s := string(b)
	lines := strings.Split(s, "\n")
	var raw string
	var bech32 string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "-----") {
			re := regexp.MustCompile("erd1[a-z0-9]{58}")
			if m := re.FindString(t); m != "" { bech32 = m }
			continue
		}
		if t == "" { continue }
		raw += t
	}
	data, _ := base64.StdEncoding.DecodeString(raw)
	var content string
	if data != nil { content = string(data) } else { content = raw }
	re := regexp.MustCompile("[0-9a-fA-F]{64}")
	match := re.FindString(content)
	var seed []byte
	if match != "" { seed, _ = hex.DecodeString(match) } else if len(data) >= 32 { seed = data[:32] }
	if seed == nil { return nil, "", fmt.Errorf("bad key") }
	return ed25519.NewKeyFromSeed(seed), bech32, nil
}

func getNonce(proxy, addr string) uint64 {
	var r struct { Data struct { Account struct { Nonce uint64 } } }
	resp, _ := http.Get(proxy+"/address/"+addr)
	if resp != nil { defer resp.Body.Close(); json.NewDecoder(resp.Body).Decode(&r) }
	return r.Data.Account.Nonce
}

func send(proxy string, tx *Transaction, priv ed25519.PrivateKey) error {
	jb, _ := json.Marshal(tx)
	sig := ed25519.Sign(priv, jb)
	tx.Signature = hex.EncodeToString(sig)
	payload, _ := json.Marshal(tx)
	resp, err := http.Post(proxy+"/transaction/send", "application/json", bytes.NewReader(payload))
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { 
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body)) 
	}
	return nil
}

func main() {
	if len(os.Args) < 3 { log.Fatal("Usage: fund <master_pem> <final_99_file>") }
	masterPem, final99File := os.Args[1], os.Args[2]
	proxy := "https://gateway.battleofnodes.com"
	
	priv, masterAddr, _ := parsePEM(masterPem)
	
	data, _ := os.ReadFile(final99File)
	lines := strings.Split(string(data), "\n")
	var targets []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if len(t) > 0 && strings.HasPrefix(t, "erd1") { targets = append(targets, t) }
	}

	nonce := getNonce(proxy, masterAddr)
	fmt.Printf("Starting ESDT funding from master %s (Nonce: %d)\n", masterAddr, nonce)

	wgldHex := hex.EncodeToString([]byte("WEGLD-bd4d79"))
	usdcHex := hex.EncodeToString([]byte("USDC-c76f1f"))
	amtW := "01bc16d674ec80000" // 2 WEGLD
	amtU := "05f5e100"          // 100M USDC

	for _, t := range targets {
		// 1. Send WEGLD
		txW := &Transaction{
			Nonce: nonce, Value: "0", Receiver: t, Sender: masterAddr,
			GasPrice: 1000000000, GasLimit: 1000000, ChainID: "B", Version: 1,
			Data: "ESDTTransfer@" + wgldHex + "@" + amtW,
		}
		if err := send(proxy, txW, priv); err == nil {
			fmt.Printf("[%s] Funded WEGLD (Nonce %d)\n", t, nonce)
			nonce++
		}
		time.Sleep(100 * time.Millisecond)

		// 2. Send USDC
		txU := &Transaction{
			Nonce: nonce, Value: "0", Receiver: t, Sender: masterAddr,
			GasPrice: 1000000000, GasLimit: 1000000, ChainID: "B", Version: 1,
			Data: "ESDTTransfer@" + usdcHex + "@" + amtU,
		}
		if err := send(proxy, txU, priv); err == nil {
			fmt.Printf("[%s] Funded USDC (Nonce %d)\n", t, nonce)
			nonce++
		}
		time.Sleep(100 * time.Millisecond)
	}
}
