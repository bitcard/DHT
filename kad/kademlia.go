package main

import (
	"DHT/utils"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	badger "github.com/ipfs/go-ds-badger"
	"github.com/libp2p/go-libp2p"
	host "github.com/libp2p/go-libp2p-host"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	dhtopts "github.com/libp2p/go-libp2p-kad-dht/opts"
	peerstore "github.com/libp2p/go-libp2p-peerstore"
	multiaddr "github.com/multiformats/go-multiaddr"
)

type query struct {
	JSONRPCMethod string
	Key           string
	Value         string
}

type response struct {
	val []byte
}

func createHost(ctx context.Context, hostAddr multiaddr.Multiaddr, db *badger.Datastore) (*dht.IpfsDHT, host.Host) {

	// In the options add the privatekey
	host, err := libp2p.New(ctx,
		libp2p.ListenAddrs([]multiaddr.Multiaddr{hostAddr}...),
		libp2p.Identity(utils.GeneratePrivateKey(time.Now().Unix())),
	)

	if err != nil {
		log.Fatal(err)
	}
	// add the DHT options

	kad, err := dht.New(ctx, host, dhtopts.Validator(utils.NullValidator{}), dhtopts.Datastore(db))
	if err != nil {
		log.Fatal(err)
	}
	return kad, host
}

func addPeers(ctx context.Context, peersList []string, h host.Host, kad *dht.IpfsDHT) {

	if len(peersList) == 0 {
		return
	}

	for _, addr := range peersList {
		peerID, peerAddr := utils.MakePeer(addr)
		h.Peerstore().AddAddr(peerID, peerAddr, peerstore.PermanentAddrTTL)
		err := kad.Ping(ctx, peerID)
		if err != nil {
			log.Println(err)
		} else {
			log.Println("peer active")
		}
		kad.Update(ctx, peerID)
	}

	return

}

func main() {
	ctx := context.Background()
	port := os.Args[1]

	// contact discovery server
	conn, err := net.Dial("tcp", os.Args[2])
	if err != nil {
		log.Fatal("Failed to query discovery server", err)
	}
	ipaddr := conn.LocalAddr().String()
	ipaddr = ipaddr[:strings.IndexByte(ipaddr, ':')]
	addr, err := utils.GenerateMultiAddr(port, ipaddr)

	db, err := badger.NewDatastore("./badgerDB"+port, nil)
	if err != nil {
		log.Fatal(err)
	}

	kad, host := createHost(ctx, addr, db)
	hostAddr := fmt.Sprintf("%s/p2p/%s", addr, host.ID().Pretty())
	log.Println(hostAddr)

	buf := []byte{0x01}
	payload := []byte(hostAddr)
	var l uint32
	l = uint32(len(payload))

	b := new(bytes.Buffer)
	binary.Write(b, binary.LittleEndian, l)
	buf = append(buf, b.Bytes()...)
	buf = append(buf, payload...)

	conn.Write(buf)

	resp := make([]byte, 1024)
	Len, _ := conn.Read(resp)

	// decoding the list of peers
	var peerAddr []string
	json.Unmarshal(resp[:Len], &peerAddr)

	log.Println(peerAddr)
	// connecting with peers
	addPeers(ctx, peerAddr, host, kad)

	if os.Args[3] == "disableLogging" {
		log.SetOutput(ioutil.Discard)
	}

	// HTTP API endpoint
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var q query
		err := json.NewDecoder(r.Body).Decode(&q)
		if err != nil {
			log.Print("error ")
			log.Println(err)
		}
		if q.JSONRPCMethod == "dht_putValue" {
			log.Println("PutValue Request")
			log.Println(q.JSONRPCMethod, q.Key, string(q.Value))
			kad.PutValue(ctx, q.Key, []byte(q.Value))
		} else if q.JSONRPCMethod == "dht_getValue" {
			log.Println("GetValue Request")
			log.Println(q.JSONRPCMethod, q.Key)
			val, err := kad.GetValue(ctx, q.Key)
			if err != nil {
				log.Println(err)
			}
			ww := response{}
			ww.val = val
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(val)
		}
	})

	httpPort, _ := strconv.Atoi(port)
	httpPort++
	str := strconv.Itoa(httpPort)

	http.ListenAndServe(":"+str, nil)

	defer db.Close()

	select {}
}
