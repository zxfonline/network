package network

import (
	"context"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/zxfonline/golog"
)

var (
	// ExitEvent 关闭处理器
	ExitEvent int32 = 1
	// AddEvent 加入连接
	AddEvent int32 = 2
	// RemoveEvent 删除连接
	RemoveEvent int32 = 3
	// CreateEvent 创建连接
	CreateEvent int32 = 4
)

//处理器处理消息类型
type ProcessType byte

const (
	ProcessTypeMessage ProcessType = 1 << iota
	ProcessTypeEvent
	ProcessTypeFunc
	ProcessTypeTick
)

// network.ReturnToClient 返回给客户端的消息
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

//消息处理器接口
type IProcessor interface {
	//GetCallbackIds 获取所有消息回调id列表
	GetCallbackIds() []int32
	//GetCallback 获取指定id消息回调
	GetCallback(id int32) MsgCallback
	//AddCallback 设置回调
	AddCallback(id int32, callback MsgCallback)
	//RemoveCallback 删除回调
	RemoveCallback(id int32)
	//AddEventCallback 事件处理函数注册
	AddEventCallback(id int32, callback EventCallback)
	//RemoveEventCallback 删除事件处理回调
	RemoveEventCallback(id int32)
	//SetTickFunc 设置更新时间，以及更新函数,第一个参数，时间的设定，只在调用StartProcess之前调用起作用
	SetTickFunc(uptime time.Duration, upcall ProcFunction)
	//StartProcess 开始处理信息，只有调用了这个接口，处理器才会处理实际的信息，以及实际发送消息
	StartProcess(ctx context.Context, wg *sync.WaitGroup, loopFun func(ProcessType, interface{}))
	//TriggerEvent 触发事件
	TriggerEvent(event *Event) bool
	//AddMessage 添加处理消息
	AddMessage(msg *Message) bool
	//ProcMessage 处理消息
	ProcMessage(msg *Message)
	//AddFunc 添加执行函数
	AddFunc(funz ProcFunction) bool
	//ImmediateMode 立即回调消息，如果想要线程安全，必须设置为false，默认为false
	ImmediateMode(state bool)
	//IsImmediate 是否是急速模式
	IsImmediate() bool
	//UnHandledHandler 通用消息处理器
	UnHandledHandler(uhandle MsgCallback)
	//IsClosed 处理器是否关闭
	IsClosed() bool
	//Close 处理器关闭
	Close()
}
