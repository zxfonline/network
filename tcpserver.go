package network

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/zxfonline/golog"

	"github.com/zxfonline/trace"
)

// Server tcp server
type Server struct {
	Listener *net.TCPListener

	Logger *golog.Logger
}

// NewTCPServer new tcp server
func NewTCPServer(logger *golog.Logger, bindAddress string) (*Server, error) {
	i := strings.Index(bindAddress, ":")
	if i < 0 {
		return nil, fmt.Errorf("Bad WanAddress: %s", bindAddress)
	}
	serverAddr, err := net.ResolveTCPAddr("tcp", bindAddress[i:])
	if err != nil {
		return nil, err
	}
	listner, err := net.ListenTCP("tcp", serverAddr)
	if err != nil {
		return nil, err
	}
	server := &Server{
		Listener: listner,
		Logger:   logger,
	}
	logger.Infof("server start:%s.", listner.Addr().String())
	return server, nil
}

// ReadMessage read a message from connection ,blocked
func ReadMessage(conn io.Reader) (*MessageHead, []byte, error) {
	buffer, err := ReadFromConnect(conn, HEAD_SIZE)
	if err != nil {
		return nil, nil, err
	}
	h := ReadHead(buffer)
	if h.ID > MaxMessageID || h.Length < 0 || h.Length > MaxMessageLength {
		golog.Warnf("message error: id(%d),len(%d)", h.ID, h.Length)
		return nil, nil, errors.New("message not in range")
	}
	//golog.Debugf("recv message : id(%d),len(%d)", h.ID, h.Length)
	buffer, err = ReadFromConnect(conn, int(h.Length))
	if err != nil {
		return nil, nil, err
	}
	return h, buffer, nil

}

//  accept
func (s *Server) BlockAccept(logger *golog.Logger, proc IProcessor, senderSize int32, sendfullClose bool, proxyTrace *trace.ProxyTrace) (err error) {
	var tempDelay time.Duration
	for {
		if err = s.BlockAcceptOne(logger, proc, senderSize, sendfullClose); err != nil {
			if !proc.IsClosed() {
				s.Logger.Warnf("tcp accept error :%s", err.Error())
			}
			if proxyTrace != nil {
				trace.TraceErrorf(proxyTrace, "Tcp accept fail:%v,err:%v", s.Listener.Addr(), err)
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				s.Logger.Warnf("tcp: Accept error:%s.retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			} else {
				return
			}
		}
		tempDelay = 0
	}
}

//  接受一个连接
func (s *Server) BlockAcceptOne(logger *golog.Logger, proc IProcessor, senderSize int32, sendfullClose bool) error {
	conn, err := s.Listener.Accept()
	if err == nil {
		s.Logger.Debugf("incomming connection %s %p.", conn.RemoteAddr(), conn)
		peer := NewClientPeer(logger, conn, proc, senderSize, sendfullClose)
		if ok := proc.TriggerEvent(&Event{
			ID:   AddEvent,
			Peer: peer,
		}); !ok {
			peer.Close()
			err = errors.New("add event error,close connect")
		}
	}
	return err
}
