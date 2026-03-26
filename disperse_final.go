package main
import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/base64"
	"math/big"
	"sync"
	"time"
	"regexp"
)
type Transaction struct {
	Nonce    uint64 `json:"nonce"`
	Value    string `json:"value"`
	Receiver string `json:"receiver"`
	Sender   string `json:"sender"`
	GasPrice uint64 `json:"gasPrice"`
	GasLimit uint64 `json:"gasLimit"`
	Data     string `json:"data,omitempty"`
	ChainID  string `json:"chainID"`
	Version  uint32 `json:"version"`
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
	
	// Try Base64 decode
	data, err := base64.StdEncoding.DecodeString(raw)
	var content string
	if err == nil { content = string(data) } else { content = raw }
	
	re := regexp.MustCompile("[0-9a-fA-F]{64}")
	match := re.FindString(content)
	var seed []byte
	if match != "" {
		seed, _ = hex.DecodeString(match)
	} else if len(data) >= 32 {
		seed = data[:32]
	} else {
		return nil, "", fmt.Errorf("bad seed")
	}
	
	priv := ed25519.NewKeyFromSeed(seed)
	if bech32 == "" {
		// Fallback: we don't have a bech32 converter here, so we hope it was in the PEM
		// For the bot, all PEMs have it.
		return nil, "", fmt.Errorf("no bech32 address in PEM comment")
	}
	return priv, bech32, nil
}
func getBalance(proxy, addr string) *big.Int {
	resp, err := http.Get(proxy + "/address/" + addr)
	if err != nil { return big.NewInt(0) }
	defer resp.Body.Close()
	var r struct { Data struct { Account struct { Balance string } } }
	json.NewDecoder(resp.Body).Decode(&r)
	b := new(big.Int)
	b.SetString(r.Data.Account.Balance, 10)
	return b
}
func getNonce(proxy, addr string) uint64 {
	resp, _ := http.Get(proxy + "/address/" + addr)
	defer resp.Body.Close()
	var r struct { Data struct { Account struct { Nonce uint64 } } }
	json.NewDecoder(resp.Body).Decode(&r)
	return r.Data.Account.Nonce
}
func send(proxy string, priv ed25519.PrivateKey, from, to string, val *big.Int, nonce uint64) {
	tx := Transaction{
		Nonce: nonce, Value: val.String(), Receiver: to, Sender: from,
		GasPrice: 2000000000, GasLimit: 50000, ChainID: "B", Version: 1,
	}
	jb, _ := json.Marshal(tx)
	sig := ed25519.Sign(priv, jb)
	tx.Signature = hex.EncodeToString(sig)
	payload, _ := json.Marshal(tx)
	http.Post(proxy + "/transaction/send", "application/json", strings.NewReader(string(payload)))
}
func main() {
	if len(os.Args) < 3 { log.Fatal("Usage: disperse <master_pem> <address_file>") }
	masterPem, addrFile := os.Args[1], os.Args[2]
	proxy := "https://gateway.battleofnodes.com"
	priv, masterAddr, err := parsePEM(masterPem)
	if err != nil { log.Fatalf("Parse Master: %v", err) }
	data, _ := os.ReadFile(addrFile)
	lines := strings.Split(string(data), "\n")
	var targets []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if len(t) > 0 { targets = append(targets, t) }
	}
	bal := getBalance(proxy, masterAddr)
	reserve := new(big.Int).Mul(big.NewInt(15), new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil)) // 0.15 EGLD reserve
	if bal.Cmp(reserve) < 0 { log.Fatalf("Master %s has only %s, need %s", masterAddr, bal.String(), reserve.String()) }
	totalToSend := new(big.Int).Sub(bal, reserve)
	share := new(big.Int).Div(totalToSend, big.NewInt(int64(len(targets))))
	fmt.Printf("Master %s has %s. Dispersing %s to %d wallets\n", masterAddr, bal.String(), share.String(), len(targets))
	nonce := getNonce(proxy, masterAddr)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(target string, n uint64) {
			defer wg.Done()
			send(proxy, priv, masterAddr, target, share, n)
		}(t, nonce)
		nonce++
		time.Sleep(100 * time.Millisecond)
	}
	wg.Wait()
}
