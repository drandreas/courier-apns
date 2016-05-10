package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"log/syslog"
	"net"
	"os"
	"path"
	"time"

	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"

	"github.com/sideshow/apns2"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	optCrt  = kingpin.Arg("crt", "Path to certificate file.").Required().String()
	optKey  = kingpin.Arg("key", "Path to private key file.").Required().String()
	optSock = kingpin.Flag("socket", "Path to use for Unix socket.").
		Default("/var/run/courier/courierapns.socket").Short('s').String()
	optSyLog = kingpin.Flag("syslog", "Use Syslog instead of STDERR.").Short('d').Bool()
	optConc  = kingpin.Flag("concurrent", "Enable concurrent request handling.").Short('c').Bool()
	topic    string //Topic is extracted from the certificate during startup
)

// The Device data structure must match the JSON
// structure stored by Courier-IMAPd.
type Device struct {
	ApsAccountId   string   `json:"aps-account-id"`
	ApsDeviceToken string   `json:"aps-device-token"`
	Mailboxes      []string `json:"mailboxes"`
}

// HandleRequest reads the path to a Maildir and performs
// a push to all devices listed in Maildir/.push/.
//
// If pushing to a device fails with HTTP status code 410,
// the coresponding subscription file is deleted.
func HandleRequest(conn net.Conn, client *apns2.Client) {
	defer conn.Close()

	// read path
	reader := bufio.NewReader(conn)
	dir, _, err := reader.ReadLine()
	if err != nil {
		log.Print("Error: ", err)
		return
	}

	// read devices
	log.Printf("Push %s\n", dir)
	files, err := ioutil.ReadDir(path.Join(string(dir), ".push"))
	if err != nil || len(files) == 0 {
		fmt.Fprintf(conn, "Push %s disabled\n", dir)
		log.Printf("  disabled\n")
		return
	} else {
		fmt.Fprintf(conn, "Push %s to", dir)
	}
	for _, file := range files {
		data, err := ioutil.ReadFile(path.Join(string(dir), ".push", file.Name()))
		if err != nil {
			log.Print("Error:", err)
			continue
		}
		device := Device{}
		err = json.Unmarshal(data, &device)
		if err != nil {
			log.Print("Error:", err)
			continue
		}

		// perform push
		notification := &apns2.Notification{
			Topic:       topic,
			DeviceToken: device.ApsDeviceToken,
			Expiration:  time.Now().Add(24 * time.Hour * 7),
			Payload: fmt.Sprintf("{ \"aps\": { \"account-id\": \"%s\" }}",
				device.ApsAccountId),
		}
		res, err := client.Push(notification)

		// handle errors
		if err != nil {
			log.Fatal("  Error: ", err)
		} else {
			fmt.Fprintf(conn, " %s (%v)", device.ApsDeviceToken[0:8], res.StatusCode)
			log.Printf("  Device: %s, Code: %v, Reason: %v\n", device.ApsDeviceToken[0:8],
				res.StatusCode, res.Reason)
			// Remove file if device token is no longer active for the topic
			if res.StatusCode == 400 ||
				(res.StatusCode == 410 && file.ModTime().Before(res.Timestamp.Time)) {
				err = os.Remove(path.Join(string(dir), ".push", file.Name()))
				if err != nil {
					log.Print("Error:", err)
				} else {
					log.Printf("  Device: %s removed\n", device.ApsDeviceToken[0:8])
				}
			}
		}
	}
	fmt.Fprintf(conn, "\n")
}

// ParsCertificate extracts OID 0.9.2342.19200300.100.1.1
// from a certificate. It must be identical to the one
// defined for Courier-IMAPd.
//
// Author: Stefan Arentz, License: MIT
// Source: https://github.com/st3fan/dovecot-xaps-daemon/
func ParsCertificate(bytes []byte) (string, error) {
	block, _ := pem.Decode(bytes)
	if block == nil {
		return "", errors.New("Could not decode PEM block from certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}

	if len(cert.Subject.Names) == 0 {
		return "", errors.New("Subject.Names is empty")
	}

	oidUid := []int{0, 9, 2342, 19200300, 100, 1, 1}
	if !cert.Subject.Names[0].Type.Equal(oidUid) {
		return "", errors.New("Did not find a Subject.Names[0] with type 0.9.2342.19200300.100.1.1")
	}

	return cert.Subject.Names[0].Value.(string), nil
}

func main() {
	kingpin.UsageTemplate(kingpin.CompactUsageTemplate).Version("0.1").Author("Andreas Dröscher")
	kingpin.CommandLine.Help = `Listens to on a Unix socket and performs mail notifications.
	This deamon supports persistent connections to APNs. The expected input on the Unix socket
	is a single line containing a path to a Maildir. The client can be as simple as a piped echo
	e.g. echo /path/to/maildir | nc.openbsd -U /var/run/courier/courierapns.socket.`
	kingpin.Parse()

	// attach Logger to Syslog
	if *optSyLog {
		logwriter, err := syslog.New(syslog.LOG_NOTICE, "courierapns")
		if err != nil {
			log.Fatal("Error:", err)
		}
		log.SetOutput(logwriter)
		log.SetFlags(0)
	}

	// load certificate and key
	log.Printf("Loading certificate...\n")
	crt, err := ioutil.ReadFile(*optCrt)
	if err != nil {
		log.Fatal("Error:", err)
	}
	key, err := ioutil.ReadFile(*optKey)
	if err != nil {
		log.Fatal("Error:", err)
	}

	// extract and output uid (oid 0.9.2342.19200300.100.1.1 see
	//                         http://tools.ietf.org/html/rfc4519#section-2.39)
	topic, err = ParsCertificate(crt)
	log.Printf("UID=%s\n", topic)
	log.Printf("Make sure your IMAP_XAPPLEPUSHSERVICE_TOPIC is set accordingly.\n")

	// establish APNs connection
	log.Printf("Establishing connection...\n")
	cred, err := tls.X509KeyPair(crt, key)
	if err != nil {
		log.Fatal("Error:", err)
	}
	client := apns2.NewClient(cred)
	client.Production()

	// open socket and serve clients
	log.Printf("Waiting for request...\n")
	os.Remove(*optSock)
	socket, err := net.Listen("unix", *optSock)
	if err != nil {
		log.Fatal("Error:", err)
	}
	err = os.Chmod(*optSock, 0777)
	if err != nil {
		log.Print("Warning:", err)
	}
	for {
		conn, err := socket.Accept()
		if err != nil {
			log.Fatal("Error:", err)
		} else {
			if *optConc {
				go HandleRequest(conn, client)
			} else {
				HandleRequest(conn, client)
			}
		}
	}
}
