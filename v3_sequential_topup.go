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
	"math/big"
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
	ChainID   string `json:"chainID"`
	Version   uint32 `json:"version"`
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
	priv := ed25519.NewKeyFromSeed(seed)
	return priv, bech32, nil
}

func getJSON(url string, target interface{}) error {
	resp, err := http.Get(url)
	if err != nil { return err }
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func getBalance(proxy, addr string) *big.Int {
	var r struct { Data struct { Account struct { Balance string } } }
	if err := getJSON(proxy+"/address/"+addr, &r); err != nil { return big.NewInt(0) }
	b := new(big.Int)
	b.SetString(r.Data.Account.Balance, 10)
	return b
}

func getNonce(proxy, addr string) uint64 {
	var r struct { Data struct { Account struct { Nonce uint64 } } }
	if err := getJSON(proxy+"/address/"+addr, &r); err != nil { return 0 }
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
	if len(os.Args) < 3 { log.Fatal("Usage: topup <master_pem> <final_99_file>") }
	masterPem, final99File := os.Args[1], os.Args[2]
	proxy := "https://gateway.battleofnodes.com"
	
	priv, masterAddr, _ := parsePEM(masterPem)
	targetBal, _ := new(big.Int).SetString("4900000000000000000", 10) // 4.9 EGLD
	
	data, _ := os.ReadFile(final99File)
	lines := strings.Split(string(data), "\n")
	var targets []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if len(t) > 0 { targets = append(targets, t) }
	}

	masterNonce := getNonce(proxy, masterAddr)
	fmt.Printf("Starting sequential top-up from master %s (Nonce: %d)\n", masterAddr, masterNonce)

	for _, t := range targets {
		bal := getBalance(proxy, t)
		if bal.Cmp(targetBal) < 0 {
			diff := new(big.Int).Sub(targetBal, bal)
			tx := &Transaction{
				Nonce: masterNonce, Value: diff.String(), Receiver: t, Sender: masterAddr,
				GasPrice: 2000000000, GasLimit: 50000, ChainID: "B", Version: 1,
			}
			if err := send(proxy, tx, priv); err == nil {
				fmt.Printf("[%s] Funded with %s (Nonce %d)\n", t, diff.String(), masterNonce)
				masterNonce++
			} else {
				fmt.Printf("[%s] ERROR: %v\n", t, err)
			}
			time.Sleep(100 * time.Millisecond)
		} else {
			fmt.Printf("[%s] Compliant: %s\n", t, bal.String())
		}
	}
    fmt.Println("Sequential top-up complete.")
}
