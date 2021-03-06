package network

import (
	"context"
	"errors"
	"net"

	"github.com/zxfonline/golog"

	"github.com/zxfonline/chanutil"
)

const (
	DEFAULT_QUEUE_SIZE = 512
)

type Sender struct {
	pending       chan []byte
	conn          net.Conn
	sendfullClose bool
	done          chanutil.DoneChan
	Logger        *golog.Logger
}

func (s *Sender) Send(data []byte) bool {
	if s.sendfullClose { //缓存管道满了会关闭连接
		select {
		case <-s.done:
			s.Logger.Debugf("sender close: %p %s,add msg false.", s, s.conn.RemoteAddr())
			return false
		case s.pending <- data:
			if wait := len(s.pending); wait > cap(s.pending)/10*5 && wait%10 == 0 {
				s.Logger.Warnf("sender send process,waitchan:%d/%d,remote %s msgid %d..", wait, cap(s.pending), s.conn.RemoteAddr(), ReadHead(data).ID)
			}
			return true
		default:
			s.Logger.Warnf("sender overflow,close conn,pending %d remote %s msgid %d.", len(s.pending), s.conn.RemoteAddr(), ReadHead(data).ID)
			s.Close()
			return false
		}
	} else {
		if wait := len(s.pending); wait > cap(s.pending)/10*5 && wait%10 == 0 {
			s.Logger.Warnf("sender send process,waitchan:%d/%d,remote %s msgid %d.", wait, cap(s.pending), s.conn.RemoteAddr(), ReadHead(data).ID)
		}
		//阻塞发送，直到管道关闭
		select {
		case s.pending <- data:
			return true
		case <-s.done:
			return false
		}
	}
}

func (s *Sender) SyncSend(data []byte) error {
	select {
	case <-s.done:
		s.Logger.Warnf("sender close: %p %s,send msg false.", s, s.conn.RemoteAddr())
		return errors.New("send to closed conn")
	default:
		return s.rawSend(data)
	}
}

func (s *Sender) PendingCnt() int {
	return len(s.pending)
}

func (s *Sender) Start(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.Logger.Errorf("sender error: %p %v ,err: %v.", s, s, r)
		}
		s.done.SetDone()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-s.pending:
			if s.rawSend(data) != nil {
				return
			}
		case <-s.done:
			s.Logger.Debugf("sender close: %p %s.", s, s.conn.RemoteAddr())
			return
		}
	}
}

func (s *Sender) rawSend(data []byte) (err error) {
	_, err = s.conn.Write(data)
	return
}

//Close 连接关闭
func (s *Sender) Close() {
	s.done.SetDone()
}

//Closed 连接是否已经关闭
func (s *Sender) Closed() bool {
	return s.done.R().Done()
}

func NewSender(logger *golog.Logger, conn net.Conn, psize int32, sendfullClose bool) *Sender {
	size := DEFAULT_QUEUE_SIZE
	if psize > 0 {
		size = int(psize)
	}
	return &Sender{
		conn:          conn,
		pending:       make(chan []byte, size),
		sendfullClose: sendfullClose,
		done:          chanutil.NewDoneChan(),
		Logger:        logger,
	}
}
