package network

import (
	"context"
	"io"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/zxfonline/golog"

	"net"

	"github.com/zxfonline/chanutil"
)

var (
	FlagType_name = map[int32]string{
		0: "Client",
		1: "CenterServer",
		2: "GameServer",
		3: "GateServer",
		4: "LoginServer",
		5: "BattleServer",
		6: "GmServer",
	}
)

type FlagType int32

func (x FlagType) String() string {
	return EnumName(FlagType_name, int32(x))
}

func EnumName(m map[int32]string, v int32) string {
	s, ok := m[v]
	if ok {
		return s
	}
	return strconv.Itoa(int(v))
}

const (
	FlagType_Client   FlagType = 0
	FlagType_Center   FlagType = 1
	FlagType_Game     FlagType = 2
	FlagType_Gate     FlagType = 3
	FlagType_Login    FlagType = 4
	FlagType_Battle   FlagType = 5
	FlagType_GmServer FlagType = 6
)

type Cipher interface {
	Encrypt(data []byte) []byte
	Decrypt(data []byte) []byte
}

var idPool uint64
func NewClientPeer(logger *golog.Logger, conn net.Conn, proc IProcessor, senderSize int32, sendfullClose bool) *ClientPeer {
	return &ClientPeer{
		Logger:        logger,
		conn:          conn,
		Proc:          proc,
		UID:           atomic.AddUint64(&idPool, 1),
		sender:        NewSender(logger, conn, senderSize, sendfullClose),
		closed:        chanutil.NewDoneChan(),
		max_recv_size: MaxMessageLength,
	}
}

type ClientPeer struct {
	Logger *golog.Logger
	conn   net.Conn
	Proc   IProcessor
	//连接id的唯一值标识，读取消息后赋值给消息UID，用于消息处理器的负载均衡
	UID    uint64
	sender *Sender
	Flag   FlagType
	done   uint32
	closed chanutil.DoneChan

	max_recv_size int32
	read_delay    time.Duration

	rpm_limit    uint32 // 包频率包数
	rpm_interval int64  // 包频率检测间隔
	off_line_msg []byte // 超过频率控制离线通知包
}

// 设置连接参数
func (peer *ClientPeer) SetParameter(read_delay time.Duration, max_recv_size int32) {
	if max_recv_size > 0 {
		peer.max_recv_size = max_recv_size
	}
	if read_delay > 0 {
		peer.read_delay = read_delay
	}
}

// 包频率控制参数
func (peer *ClientPeer) SetRpmParameter(rpm_limit uint32, rpm_interval int64, msg []byte) {
	peer.rpm_limit = rpm_limit
	peer.rpm_interval = rpm_interval
	peer.off_line_msg = msg
}

func (peer *ClientPeer) RemoteAddr() net.Addr {
	return peer.conn.RemoteAddr()
}

//网络连接远程ip
func (peer *ClientPeer) NetIp() string {
	addr := peer.conn.RemoteAddr().String()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return net.ParseIP(host).String()
}

//连接是否已经关闭
func (peer *ClientPeer) IsClosed() bool {
	return peer.closed.R().Done()
}

func (peer *ClientPeer) Close() {
	if peer.closed.R().Done() {
		return
	}
	peer.closed.SetDone()
	peer.sender.Close()
	peer.conn.Close()
}

func (peer *ClientPeer) SyncSendMessage(msg proto.Message, msgid int32) (ok bool) {
	defer func() {
		if err := recover(); err != nil {
			peer.Logger.Errorf("SyncSendMessage msgid:%d recover:%v", msgid, err)
		}
	}()
	if buf, err := GetMessageBuffer(msg, msgid); err == nil {
		if err := peer.sender.SyncSend(buf); err != nil {
			peer.Logger.Warnf("SyncSendMessage msg:%d err:%s", msgid, err.Error())
		} else {
			ok = true
		}
	} else {
		peer.Logger.Warnf("SyncSendMessage msg:%d err:%s", msgid, err.Error())
	}
	return
}

func (peer *ClientPeer) SendMessage(msg proto.Message, msgid int32) (ok bool) {
	defer func() {
		if err := recover(); err != nil {
			peer.Logger.Errorf("SendMessage msgid:%d recover:%v", msgid, err)
		}
	}()
	if buf, err := GetMessageBuffer(msg, msgid); err == nil {
		ok = peer.sender.Send(buf)
	} else {
		peer.Logger.Warnf("SendMessage msg:%d err:%s", msgid, err.Error())
	}
	return
}

func (peer *ClientPeer) SendMessageBuffer(buf []byte) (ok bool) {
	defer func() {
		if err := recover(); err != nil {
			peer.Logger.Errorf("SendMessageBuffer recover:%v.", err)
		}
	}()
	ok = peer.sender.Send(buf)
	return
}

func (peer *ClientPeer) SyncSendMessageBuffer(buf []byte) (ok bool) {
	defer func() {
		if x := recover(); x != nil {
			peer.Logger.Errorf("SyncSendMessageBuffer recover:%v", x)
		}
	}()
	if err := peer.sender.SyncSend(buf); err != nil {
		peer.Logger.Warnf("SyncSendMessageBuffer err:%s", err.Error())
	} else {
		ok = true
	}
	return
}

// Start read messages here
func (peer *ClientPeer) Start(ctx context.Context) {
	if !atomic.CompareAndSwapUint32(&peer.done, 0, 1) {
		return
	}
	defer func() {
		if x := recover(); x != nil {
			peer.Logger.Errorf("recover error:%v.", x)
		}
		peer.Close()
		peer.Proc.TriggerEvent(&Event{
			ID:   RemoveEvent,
			Peer: peer,
		})
		peer.Logger.Debugf("lost connection %s %p.", peer.conn.RemoteAddr(), peer.conn)
	}()
	go peer.sender.Start(ctx)
	// 8字节包长度
	header := make([]byte, HEAD_SIZE)

	rpmstart := time.Now().Unix()
	rpmCount := uint32(0)

	rpmMsgCount := 0
	for {
		// 读取超时，不需设置写超时，sender缓存来控制写超时的关闭状态
		if peer.read_delay > 0 {
			peer.conn.SetReadDeadline(time.Now().Add(peer.read_delay))
		}
		n, err := io.ReadFull(peer.conn, header)
		if err != nil || n != HEAD_SIZE {
			// if err != io.EOF {
			// 	peer.Logger.Warnf("error receiving header,conn:%s bytes:%d size:%d err:%v", peer.NetIp(), n, HEAD_SIZE, err)
			// }
			return
		}
		size := int32(ServerEndian.Uint32(header[:4]))
		id := int32(ServerEndian.Uint32(header[4:]))
		if id < 0 || id > MaxMessageID || size < 0 || size > peer.max_recv_size {
			peer.Logger.Warnf("error parse header,conn:%s id:%d size:%d", peer.NetIp(), id, size)
			return
		}
		data := make([]byte, size)
		n, err = io.ReadFull(peer.conn, data)
		if err != nil || n != int(size) {
			// if err != io.EOF {
			// 	peer.Logger.Warnf("error receiving body,conn:%s bytes:%d size:%d err:%v", peer.NetIp(), n, size, err)
			// }
			return
		}
		// 收包频率控制
		if peer.rpm_limit > 0 {
			rpmCount++
			// 达到限制包数
			if rpmCount > peer.rpm_limit {
				now := time.Now().Unix()
				// 检测时间间隔
				if now-rpmstart < peer.rpm_interval {
					if len(peer.off_line_msg) > 0 {
						// 发送频率过高的消息包
						peer.SyncSendMessageBuffer(peer.off_line_msg)
					}
					// 提示操作太频繁三次后踢下线
					rpmMsgCount++
					if rpmMsgCount > 3 {
						peer.Logger.Warnf("conn RPM too high %d in %d s,conn:%s", rpmCount, peer.rpm_interval, peer.NetIp())
						return
					}
				}
				// 未超过限制
				rpmCount = 0
				rpmstart = now
			}
		}
		peer.Proc.AddMessage(&Message{
			Peer: peer,
			Head: &MessageHead{
				Length: int32(size),
				ID:     int32(id),
			},
			MSG: data,
			UID: peer.UID,
		})
	}
}
