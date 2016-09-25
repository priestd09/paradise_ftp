package server

import "fmt"
import "syscall"
import "net"
import "time"
import "os"
import "os/signal"
import "os/exec"
import "github.com/andrewarrow/paradise_ftp/paradise"
import "crypto/tls"

var Settings ParadiseSettings
var Listener net.Listener
var ListenerTLS net.Listener
var err error
var FinishAndStop bool

func genClientID() string {
	random, _ := os.Open("/dev/urandom")
	b := make([]byte, 16)
	random.Read(b)
	random.Close()
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func signalHandler() {
	ch := make(chan os.Signal, 10)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGUSR2)
	for {
		sig := <-ch
		switch sig {
		case syscall.SIGTERM:
			signal.Stop(ch)
			FinishAndStop = true
			return
		case syscall.SIGUSR2:
			file, _ := Listener.(*net.TCPListener).File()
			path := Settings.Exec
			args := []string{
				"-graceful"}
			cmd := exec.Command(path, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.ExtraFiles = []*os.File{file}
			err := cmd.Start()
			fmt.Println("forking err is ", err)
		}
	}
}

func handleSecureListener() {
	for {
		if FinishAndStop {
			break
		}
		ListenerTLS.(*net.TCPListener).SetDeadline(time.Now().Add(60 * time.Second))
		connection, err := ListenerTLS.Accept()
		if err != nil {
			if opError, ok := err.(*net.OpError); !ok || !opError.Timeout() {
				fmt.Println("listening error ", err)
			}
		} else {
			cid := genClientID()
			p := NewParadise(connection, cid, time.Now().Unix())
			ConnectionMap[cid] = p

			go p.HandleCommands()
		}
	}
}

func Start(fm *paradise.FileManager, am *paradise.AuthManager, gracefulChild bool) {
	Settings = ReadSettings()
	FinishAndStop = false
	fmt.Println("starting...")
	FileManager = fm
	AuthManager = am
	var errTLS error

	if gracefulChild {
		f := os.NewFile(3, "") // FD 3 is special number
		Listener, err = net.FileListener(f)
	} else {
		url := fmt.Sprintf("%s:%d", Settings.Host, Settings.Port)
		secure_url := fmt.Sprintf("%s:%d", Settings.Host, Settings.SecurePort)
		Listener, err = net.Listen("tcp", url)

		// openssl req -new -nodes -x509 -out server.pem -keyout server.key -days 3650 -subj "/C=DE/ST=NRW/L=Earth/O=Random Company/OU=IT/CN=www.random.com/emailAddress=foo@foo.com"
		cert, cerr := tls.LoadX509KeyPair("server.pem", "server.key")
		if cerr != nil {
			fmt.Println(cerr)
			return
		}
		config := tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.VerifyClientCertIfGiven,
			ServerName:   "localhost"}
		ListenerTLS, errTLS = tls.Listen("tcp", secure_url, &config)
	}

	if err != nil || errTLS != nil {
		fmt.Println("cannot listen: ", err, errTLS)
		return
	}
	fmt.Println("listening...")

	if gracefulChild {
		parent := syscall.Getppid()
		syscall.Kill(parent, syscall.SIGTERM)
	}

	go signalHandler()

	go handleSecureListener()

	for {
		if FinishAndStop {
			break
		}
		Listener.(*net.TCPListener).SetDeadline(time.Now().Add(60 * time.Second))
		connection, err := Listener.Accept()
		if err != nil {
			if opError, ok := err.(*net.OpError); !ok || !opError.Timeout() {
				fmt.Println("listening error ", err)
			}
		} else {
			cid := genClientID()
			p := NewParadise(connection, cid, time.Now().Unix())
			ConnectionMap[cid] = p

			go p.HandleCommands()
		}
	}

	// TODO add wait group for still active connections to finish up
	// otherwise, this will just exit and kill them
	// defeating whole point of gracefulChild restart
}
