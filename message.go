package network

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/zxfonline/golog"

	"github.com/golang/protobuf/proto"
)

var (
	ServerEndian = binary.LittleEndian
)

const (
	//MaxMessageLength 预估值
	MaxMessageLength int32 = 1024 * 1024
	//MaxMessageID 内部约定
	MaxMessageID int32 = 100000
	//HEAD_SIZE 消息头长度
	HEAD_SIZE = 8
)

// MessageHead the message head length and id
type MessageHead struct {
	Length int32
	//ID>0
	ID int32
}

func NewMessage(uid int64, peer *ClientPeer, body []byte) *Message {
	msg := &Message{
		UID:  uid,
		Peer: peer,
		Head: &MessageHead{},
	}
	if len(body) > 0 {
		return msg.Package(body)
	}
	return msg
}

func LogRecvMsg(msg *Message, s proto.Message) {
	if s != nil {
		golog.Debugf("user(%d) handle msgid:%d,msg:%s.", msg.UID, msg.Head.ID, s)
	} else {
		golog.Debugf("user(%d) handle msgid:%d.", msg.UID, msg.Head.ID)
	}
}

// Message 消息链
type Message struct {
	Head *MessageHead
	MSG  []byte

	UID  int64
	Peer *ClientPeer
}

func (m *Message) Bytes() []byte {
	buff := bytes.NewBuffer(make([]byte, 0, HEAD_SIZE+len(m.MSG)))
	binary.Write(buff, ServerEndian, m.Head.Length)
	binary.Write(buff, ServerEndian, m.Head.ID)
	buff.Write(m.MSG)
	return buff.Bytes()
}
func (m *Message) Package(body []byte) *Message {
	buf := bytes.NewBuffer(body)
	binary.Read(buf, ServerEndian, &m.Head.Length)
	binary.Read(buf, ServerEndian, &m.Head.ID)
	m.MSG = buf.Bytes()
	return m
}

// 把msg转成buffer
func GetMessageBuffer(msg proto.Message, msgid int32) ([]byte, error) {
	if data, err := proto.Marshal(msg); err != nil {
		return nil, err
	} else {
		buff := bytes.NewBuffer(make([]byte, 0, HEAD_SIZE+len(data)))
		binary.Write(buff, ServerEndian, int32(len(data)))
		binary.Write(buff, ServerEndian, int32(msgid))
		buff.Write(data)
		return buff.Bytes(), nil
	}
}

//  读取消息体
func ReadFromConnect(conn io.Reader, length int) ([]byte, error) {
	data := make([]byte, length)
	len, err := io.ReadFull(conn, data)
	if err == nil && len == length {
		return data, nil
	}
	return nil, err
}

// 读取消息头
func ReadHead(src []byte) *MessageHead {
	buf := bytes.NewBuffer(src)
	var head MessageHead
	binary.Read(buf, ServerEndian, &head.Length)
	binary.Read(buf, ServerEndian, &head.ID)
	return &head
}
