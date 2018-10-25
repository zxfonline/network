package network

import (
	"context"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/zxfonline/golog"
)

var (
	// AddEvent 加入连接
	AddEvent int32 = 2
	// RemoveEvent 删除连接
	RemoveEvent int32 = 3
	// CreateEvent 创建连接
	CreateEvent int32 = 4
)

//ProcessType 处理器处理消息类型
type ProcessType byte

const (
	//ProcessTypeMessage 消息处理
	ProcessTypeMessage ProcessType = 1 << iota
	//ProcessTypeEvent 事件处理
	ProcessTypeEvent
	//ProcessTypeFunc 函数处理
	ProcessTypeFunc
	//ProcessTypeTick 定时函数处理
	ProcessTypeTick
)

// ReturnToClient 返回给客户端的消息
type ReturnToClient struct {
	ID  int32
	MSG proto.Message
}

// MsgCallback 消息处理函数
type MsgCallback func(msg *Message, logger *golog.Logger) []*ReturnToClient

// EventCallback 时间处理
type EventCallback func(ctx context.Context, event *Event)

// ProcFunction 回掉的程序
type ProcFunction func()

// Event 自定义事件
type Event struct {
	ID     int32       //eventid
	Param  interface{} //param
	Param2 interface{} //param2
	Peer   *ClientPeer //事件中的peer,可以为nil
}

//IProcessor 消息处理器接口
type IProcessor interface {
	//GetCallbackIds 获取所有消息回调id列表
	GetCallbackIds() []int32
	//GetCallback 获取指定id消息回调
	GetCallback(id int32) MsgCallback
	//AddCallback 设置回调 在调用 StartProcess 或 MultStartProcess 之前调用起作用
	AddCallback(id int32, callback MsgCallback)
	//AddEventCallback 事件处理函数注册 在调用 StartProcess 或 MultStartProcess 之前调用起作用
	AddEventCallback(id int32, callback EventCallback)
	//SetTickFunc 设置更新时间，以及更新函数,第一个参数，时间的设定，只在调用 StartProcess 或 MultStartProcess 之前调用起作用
	SetTickFunc(uptime time.Duration, upcall ProcFunction)
	//StartProcess 同步处理信息，只有调用了这个接口，处理器才会处理实际的信息，以及实际发送消息
	StartProcess(ctx context.Context, wg *sync.WaitGroup, loopFun func(ProcessType, interface{}))
	//MultStartProcess 并发处理信息，只有调用了这个接口，处理器才会处理实际的信息，以及实际发送消息 并发数量multSize <2 使用处理器num-1
	MultStartProcess(ctx context.Context, wg *sync.WaitGroup, multSize int)
	//TriggerEvent 触发事件
	TriggerEvent(event *Event) bool
	//AddMessage 添加处理消息
	AddMessage(msg *Message) bool
	//AddFunc 添加执行函数
	AddFunc(funz ProcFunction) bool
	//UnHandledHandler 通用消息处理器 在调用 StartProcess 或 MultStartProcess 之前调用起作用
	UnHandledHandler(uhandle MsgCallback)
	//IsClosed 处理器是否关闭
	IsClosed() bool
	//Close 处理器关闭
	Close()
}
