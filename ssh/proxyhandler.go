package ssh

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/notion/trove_ssh_bastion/config"
	"golang.org/x/crypto/ssh"
)

// ProxyHandler is the base handler for an SSH Connection and Proxy
type ProxyHandler struct {
	net.Conn
	config *ssh.ServerConfig
	env    *config.Env
}

// Serve handles the proxy
func (p *ProxyHandler) Serve() {
	clientConn, clientChans, clientReqs, err := ssh.NewServerConn(p, p.config)
	if err != nil {
		p.env.Red.Println("Failed to process handshake", err)
		return
	}

	metaInterface, ok := p.env.SSHProxyClients.Load(p.RemoteAddr().String())
	meta := metaInterface.(*config.SSHProxyClient)
	if !ok {
		p.env.Red.Println("Unable to find SSH Client to connect to server connection.")
		return
	}

	proxyConn := meta.SSHClient

	go ssh.DiscardRequests(clientReqs)

	for openedChannel := range clientChans {
		proxyChannel, proxyReqs, err := proxyConn.OpenChannel(openedChannel.ChannelType(), openedChannel.ExtraData())
		if err != nil {
			p.env.Red.Println("Couldn't accept channel on proxy:", err)
			return
		}

		clientChannel, clientReqs2, err := openedChannel.Accept()
		if err != nil {
			p.env.Red.Println("Couldn't accept channel on client:", err)
			return
		}

		chanInfo := &config.ConnChan{
			ChannelType: openedChannel.ChannelType(),
			ChannelData: openedChannel.ExtraData(),
			Reqs:        make([]*config.ConnReq, 0),
			ClientConn:  clientConn,
			ProxyConn:   proxyConn,
			ProxyChan:   &proxyChannel,
			ClientChan:  &clientChannel,
		}
		meta.SSHChans = append(meta.SSHChans, chanInfo)

		go func() {

		r:
			for {
				var req *ssh.Request
				var dst ssh.Channel

				select {
				case req = <-clientReqs2:
					dst = proxyChannel
				case req = <-proxyReqs:
					dst = clientChannel
				}

				if req == nil || dst == nil {
					break r
				}

				b, err := dst.SendRequest(req.Type, req.WantReply, req.Payload)
				if err != nil {
					p.env.Red.Println("Error sending request through channel:", err)
				}

				if req.WantReply {
					req.Reply(b, nil)
				}

				reqInfo := &config.ConnReq{
					ReqType:  req.Type,
					ReqData:  req.Payload,
					ReqReply: req.WantReply,
				}

				meta.Mutex.Lock()
				chanInfo.Reqs = append(chanInfo.Reqs, reqInfo)
				meta.Mutex.Unlock()

				switch req.Type {
				case "shell":
					meta.Mutex.Lock()
					meta.SSHShellSessions = append(meta.SSHShellSessions, chanInfo)
					meta.Mutex.Unlock()
				case "exit-status":
					break r
				}
			}

			proxyChannel.Close()
			clientChannel.Close()
		}()

		var wrappedClientChannel io.ReadCloser = clientChannel
		var wrappedProxyChannel = config.NewAsciicastReadCloser(proxyChannel, clientConn, 80, 40, chanInfo, p.env)

		closeChans := func() {
			wrappedClientChannel.Close()
			wrappedProxyChannel.Close()

			proxyChannel.Close()
			clientChannel.Close()
		}

		allClose := func() {
			closeChans()
		}

		var once sync.Once
		go func() {
			io.Copy(clientChannel, wrappedProxyChannel)
			timer := time.NewTimer(1 * time.Second)
			<-timer.C
			once.Do(allClose)
		}()
		go func() {
			io.Copy(proxyChannel, wrappedClientChannel)
			timer := time.NewTimer(1 * time.Second)
			<-timer.C
			once.Do(allClose)
		}()

		defer once.Do(allClose)
	}

	p.env.Magenta.Println("Closed proxy connection.")
}
