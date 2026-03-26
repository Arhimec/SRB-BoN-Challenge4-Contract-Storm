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
	"math/big"
	"sync"
	"time"
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
	lines := strings.Split(string(b), "\n")
	var hexSeed string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "-----") || t == "" {
			continue
		}
		hexSeed = t
		break
	}
	seed, _ := hex.DecodeString(hexSeed)
	if len(seed) != 32 { return nil, "", fmt.Errorf("bad seed") }
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return priv, hex.EncodeToString(pub), nil
}
func getBalance(proxy, addr string) *big.Int {
	resp, _ := http.Get(proxy + "/address/" + addr)
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
		GasPrice: 1000000000, GasLimit: 50000, ChainID: "T", Version: 1,
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
	priv, masterAddr, _ := parsePEM(masterPem)
	data, _ := os.ReadFile(addrFile)
	lines := strings.Split(string(data), "\n")
	var targets []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if len(t) > 0 { targets = append(targets, t) }
	}
	bal := getBalance(proxy, masterAddr)
	reserve := new(big.Int).Mul(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(17), nil)) // 0.1 EGLD
	totalToSend := new(big.Int).Sub(bal, reserve)
	share := new(big.Int).Div(totalToSend, big.NewInt(int64(len(targets))))
	fmt.Printf("Master %s has %s. Share for %d wallets: %s\n", masterAddr, bal.String(), len(targets), share.String())
	nonce := getNonce(proxy, masterAddr)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(target string, n uint64) {
			defer wg.Done()
			send(proxy, priv, masterAddr, target, share, n)
			fmt.Printf("Sent %s to %s (nonce %d)\n", share.String(), target, n)
		}(t, nonce)
		nonce++
		time.Sleep(150 * time.Millisecond)
	}
	wg.Wait()
}
