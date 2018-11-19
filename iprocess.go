package network

import (
	"context"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/zxfonline/expvar"
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
	StartProcess(ctx context.Context, wg *sync.WaitGroup, loopFun func(ProcessType, interface{}), name string)
	/*
	   MultStartProcess 并发处理信息，只有调用了这个接口，处理器才会处理实际的信息，以及实际发送消息
	   processor_mult_size:并发数量(当值<2 使用处理器num-1) processor_balance_type={0,2}：推荐=NumCPU()； processor_balance_type={1}：服务器id-->根据和自己连接的服务器数量+1、玩家数量-->推荐=NumCPU()
	   processor_balance_type:并发处理负载类型 0：基于消息自动序列号平均负载(默认情况)；1：基于连接的唯一id进行负载(保证对应id的消息顺序执行，需要考虑动态新增服务器的情况，预留新增的数量processor_mult_size+X)；2：基于消息ID类型(消息号定义最好顺序使用，保证能够负载均衡) 算法：balanceChan[typeid%processor_mult_size]<-msg
	*/
	MultStartProcess(ctx context.Context, wg *sync.WaitGroup, processor_mult_size uint32, processor_balance_type uint32, name string)
	//TriggerEvent 触发事件
	TriggerEvent(event *Event) bool
	//AddMessage 添加处理消息
	AddMessage(msg *Message) bool
	//AddFunc 添加执行函数
	AddFunc(funz ProcFunction) bool
	//UnHandledHandler 通用消息处理器  在调用 StartProcess 或 MultStartProcess 之前调用起作用
	UnHandledHandler(uhandle MsgCallback)
	//IsClosed 处理器是否关闭
	IsClosed() bool
	//Close 处理器关闭
	Close()
	//日志处理器
	GetLogger() *golog.Logger
}
