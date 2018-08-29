package config

import (
	"bytes"
	"cloud.google.com/go/storage"
	"compress/gzip"
	"context"
	"github.com/fatih/color"
	"github.com/gorilla/websocket"
	"github.com/notion/trove_ssh_bastion/asciicast"
	"golang.org/x/crypto/ssh"
	"io"
	"log"
	"time"
)

func NewAsciicastReadCloser(r io.ReadCloser, conn ssh.ConnMetadata, width int, height int, env *Env) io.ReadCloser {
	closer := &AsciicastReadCloser{
		ReadCloser: r,
		SshConn:    conn,
		Time:       time.Now(),
		Cast: &asciicast.Cast{
			Header: &asciicast.Header{
				Version:   2,
				Width:     width,
				Height:    height,
				Timestamp: time.Now().Unix(),
			},
		},
		Env: env,
	}

	if env.LogsBucket != nil {
		bkt := env.LogsBucket

		ctx := context.Background()

		objHandler := bkt.Object(closer.Time.Format("2006-01-02 15:04:05") + " " + conn.RemoteAddr().String())
		w := objHandler.NewWriter(ctx)

		closer.BkWriter = w
		closer.BkContext = ctx

		closer.GZWriter = gzip.NewWriter(w)

		marshalledHeader, err := closer.Cast.Header.Marshal()
		if err != nil {
			log.Println("Error marshalling header", err)
		}

		marshalledHeader = append(marshalledHeader, []byte("\n")...)

		_, err = closer.GZWriter.Write(marshalledHeader)
		if err != nil {
			log.Println("Error writing header to bucket object", err)
		}
	}

	if client, ok := env.SshProxyClients[conn.RemoteAddr().String()]; ok {
		env.SshProxyClients[conn.RemoteAddr().String()].Closer = closer
		closer.User = client.SshServerClient.User
		closer.Host = client.SshServerClient.ProxyTo
	}

	return closer
}

type AsciicastReadCloser struct {
	io.ReadCloser

	SshConn   ssh.ConnMetadata
	Cast      *asciicast.Cast
	Time      time.Time
	Buffer    bytes.Buffer
	Env       *Env
	BkWriter  *storage.Writer
	BkContext context.Context
	GZWriter  *gzip.Writer
	User      *User
	Host      string
}

func (lr *AsciicastReadCloser) Read(p []byte) (n int, err error) {
	n, err = lr.ReadCloser.Read(p)

	now := time.Now()
	duration := now.Sub(lr.Time).Seconds()

	readBytes := p[0:n]

	if len(string(readBytes)) == 0 {
		return n, err
	}

	newFrame := &asciicast.Frame{
		Time:  duration,
		Event: "o",
		Data:  string(readBytes),
	}

	marshalledFrame, err := newFrame.Marshal()
	if err != nil {
		log.Println("Error marshalling frame", err)
	}

	marshalledFrame = append(marshalledFrame, []byte("\n")...)

	_, err = lr.GZWriter.Write(marshalledFrame)
	if err != nil {
		log.Println("Error writing frame to bucket object", err)
	}

	pathKey := lr.SshConn.RemoteAddr().String()

	if _, ok := lr.Env.SshProxyClients[pathKey]; ok {
		if _, ok := lr.Env.WebsocketClients[pathKey]; ok {
			for _, v := range lr.Env.WebsocketClients[pathKey] {
				wsClient := v.Client
				wsWriter, err := wsClient.NextWriter(websocket.TextMessage)
				if err != nil {
					color.Set(color.FgRed)
					log.Println("wsWriter error:", err)
					color.Unset()
				} else {
					wsWriter.Write(readBytes)
					wsWriter.Close()
				}
			}
		}
	}

	lr.Cast.Frames = append(lr.Cast.Frames, newFrame)

	return n, err
}

func (lr *AsciicastReadCloser) Close() error {
	data, err := lr.Cast.Marshal()
	if err != nil {
		log.Println("Error logging session", err)
	}

	session := &Session{
		Name: lr.BkWriter.Name,
		Time: lr.Time,
		Cast: data,
		Host: lr.Host,
	}

	if lr.User != nil {
		session.UserID = lr.User.ID
	}

	lr.Env.DB.Save(session)

	err = lr.GZWriter.Close()
	err = lr.BkWriter.Close()
	if err != nil {
		log.Println("Error closing bucket writer", err)
	}

	return lr.ReadCloser.Close()
}