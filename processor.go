package network

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/zxfonline/expvar"

	"github.com/zxfonline/golog"
	"github.com/zxfonline/timefix"

	"github.com/zxfonline/chanutil"
	"github.com/zxfonline/trace"
)

// Processor 消息处理器
type Processor struct {
	messageChan      chan *Message
	eventChan        chan *Event
	funcChan         chan ProcFunction
	callbackMap      map[int32]MsgCallback
	unHandledHandler MsgCallback //未注册的消息处理
	eventCallback    map[int32]EventCallback
	updateCallback   ProcFunction
	// 更新时间
	loopTime time.Duration
	//阻塞检测开关
	done chanutil.DoneChan

	Logger *golog.Logger
}

// NewProcessor 新建处理器，包含初始化操作
func NewProcessor(logger *golog.Logger) IProcessor {
	now := timefix.CurrentTime()
	nextTime := timefix.NextMidnight(now, 1)
	return NewProcessorWithLoopTime(logger, nextTime.Sub(now))
}

// NewProcessorWithLoopTime 指定定时器
func NewProcessorWithLoopTime(logger *golog.Logger, time time.Duration) IProcessor {
	p := &Processor{
		messageChan:   make(chan *Message, 1000),
		eventChan:     make(chan *Event, 1000),
		funcChan:      make(chan ProcFunction, 1000),
		eventCallback: make(map[int32]EventCallback),
		callbackMap:   make(map[int32]MsgCallback),
		loopTime:      time,
		done:          chanutil.NewDoneChan(),
		Logger:        logger,
	}
	return p
}

//IsClosed 处理器是否关闭
func (p *Processor) IsClosed() bool {
	return p.done.R().Done()
}

//Close 处理器关闭
func (p *Processor) Close() {
	p.done.SetDone()
}

//GetCallbackIds 获取所有消息回调id列表
func (p *Processor) GetCallbackIds() []int32 {
	list := make([]int32, 0, len(p.callbackMap))
	for id := range p.callbackMap {
		list = append(list, id)
	}
	return list
}

//AddCallback 设置回调
func (p *Processor) AddCallback(id int32, callback MsgCallback) {
	if id != 0 {
		p.callbackMap[id] = callback
	}
}

//GetCallback 获取指定id消息回调
func (p *Processor) GetCallback(id int32) MsgCallback {
	return p.callbackMap[id]
}

//SetTickFunc 设置更新时间，以及更新函数,第一个参数，时间的设定，只在调用StartProcess之前调用起作用
func (p *Processor) SetTickFunc(uptime time.Duration, upcall ProcFunction) {
	p.loopTime = uptime
	p.updateCallback = upcall
}

//AddEventCallback 事件处理函数注册
func (p *Processor) AddEventCallback(id int32, callback EventCallback) {
	p.eventCallback[id] = callback
}

//AddFunc 添加执行函数
func (p *Processor) AddFunc(funz ProcFunction) bool {
	select {
	case <-p.done:
		return false
	default:
		select {
		case <-p.done:
			return false
		case p.funcChan <- funz:
			if wait := len(p.funcChan); wait > cap(p.funcChan)/10*5 && wait%100 == 0 {
				p.Logger.Warnf("processor funcChan process,waitchan:%d/%d.", wait, cap(p.funcChan))
			}
			return true
		}
	}
}

//TriggerEvent 触发事件
func (p *Processor) TriggerEvent(event *Event) bool {
	select {
	case <-p.done:
		return false
	default:
		select {
		case <-p.done:
			return false
		case p.eventChan <- event:
			if wait := len(p.eventChan); wait > cap(p.eventChan)/10*5 && wait%100 == 0 {
				p.Logger.Warnf("processor eventChan process,waitchan:%d/%d.", wait, cap(p.eventChan))
			}
			return true
		}
	}
}

//AddMessage 添加处理消息
func (p *Processor) AddMessage(msg *Message) bool {
	select {
	case <-p.done:
		return false
	default:
		select {
		case <-p.done:
			return false
		case p.messageChan <- msg:
			if wait := len(p.messageChan); wait > cap(p.messageChan)/10*5 && wait%100 == 0 {
				p.Logger.Warnf("processor messageChan process,waitchan:%d/%d.", wait, cap(p.messageChan))
			}
			return true
		}
	}
}
func (p *Processor) procMessage(msg *Message) {
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
	}()
	if msg.Peer.IsClosed() {
		p.Logger.Debugf("conn closed,processor ignore cached message,uid:%d,msg:%d.", msg.UID, msg.Head.ID)
		return
	}
	msgName := fmt.Sprintf("MsgID:%d", msg.Head.ID)
	proxyTrace := trace.TraceStart("Processor", msgName, true)
	defer trace.TraceFinishWithExpvar(proxyTrace, func(req *expvar.Map, time int64) {
		req.AddMessage(msgName, 1, time)
	})
	var rets []*ReturnToClient
	if cb, ok := p.callbackMap[msg.Head.ID]; ok {
		//消息回执列表返回给默认连接
		rets = cb(msg, p.Logger)
	} else if p.unHandledHandler != nil {
		rets = p.unHandledHandler(msg, p.Logger)
	} else {
		p.Logger.Warnf("can't find callback(%d)", msg.Head.ID)
	}
	if len(rets) > 0 && msg.Peer != nil {
		for _, response := range rets {
			msg.Peer.SendMessage(response.MSG, response.ID)
		}
	}
}

//UnHandledHandler 通用消息处理器
func (p *Processor) UnHandledHandler(handle MsgCallback) {
	p.unHandledHandler = handle
}

// StartProcess 开始处理信息，只有调用了这个接口，处理器才会处理实际的信息，以及实际发送消息
func (p *Processor) StartProcess(ctx context.Context, wg *sync.WaitGroup, loopFun func(ProcessType, interface{})) {
	wg.Add(1)
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
		p.done.SetDone()
		wg.Done()
		p.Logger.Infof("sync processor is stoped.")
	}()
	p.Logger.Infof("sync processor is starting.")

	proxyTrace := trace.TraceStart("Goroutine", "SyncProcessor", false)
	defer trace.TraceFinish(proxyTrace)
	tick := time.Tick(p.loopTime)
	for {
		select {
		case <-p.done:
			return
		case <-ctx.Done():
			return
		case msg := <-p.messageChan:
			if loopFun != nil {
				loopFun(ProcessTypeMessage, msg)
			}
			p.procMessage(msg)
		case event := <-p.eventChan:
			if loopFun != nil {
				loopFun(ProcessTypeEvent, event)
			}
			if cb, ok := p.eventCallback[event.ID]; ok {
				recoverEventCallback(p, cb, ctx, event)
			}
		case f := <-p.funcChan:
			if loopFun != nil {
				loopFun(ProcessTypeFunc, f)
			}
			recoverFunc(p, f)
		case <-tick:
			if p.updateCallback != nil {
				if loopFun != nil {
					loopFun(ProcessTypeTick, p.updateCallback)
				}
				recoverFunc(p, p.updateCallback)
			}
		}
	}
}

//MultStartProcess 并发处理信息，只有调用了这个接口，处理器才会处理实际的信息，以及实际发送消息 并发数量multSize <2 使用处理器num-1
func (p *Processor) MultStartProcess(ctx context.Context, wg *sync.WaitGroup, multSize int) {
	if multSize < 2 {
		multSize = runtime.NumCPU() - 1
	}
	if multSize < 2 {
		multSize = 2
	}
	wg.Add(1)
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
		p.done.SetDone()
		wg.Done()
		p.Logger.Infof("mult processor is stoped.")
	}()
	p.Logger.Infof("mult processor is starting.")

	proxyTrace := trace.TraceStart("Goroutine", "MultProcessor", false)
	defer trace.TraceFinish(proxyTrace)

	balanceChanArray := make([]chan *Message, multSize)
	for i := 0; i < multSize; i++ {
		balanceChanArray[i] = make(chan *Message, 500)
	}
	waitD := chanutil.NewDoneChan()
	go func() {
		for q := false; !q; {
			select {
			case <-ctx.Done():
				waitD.SetDone()
				q = true
			case <-p.done:
				waitD.SetDone()
				q = true
			}
		}
	}()
	for i := 0; i < multSize; i++ {
		go p.balanceProcess(wg, waitD, balanceChanArray[i])
	}
	mid := 0
	tick := time.Tick(p.loopTime)
	for {
		select {
		case <-p.done:
			return
		case <-ctx.Done():
			return
		case msg := <-p.messageChan: //并发处理请求消息，其他类型还是在for中执行
			balanceChanArray[mid%multSize] <- msg
			mid++
		case event := <-p.eventChan:
			if cb, ok := p.eventCallback[event.ID]; ok {
				recoverEventCallback(p, cb, ctx, event)
			}
		case f := <-p.funcChan:
			recoverFunc(p, f)
		case <-tick:
			if p.updateCallback != nil {
				recoverFunc(p, p.updateCallback)
			}
		}
	}
}

func (p *Processor) balanceProcess(wg *sync.WaitGroup, waitD chanutil.DoneChan, msgChan <-chan *Message) {
	wg.Add(1)
	defer wg.Done()
	for {
		select {
		case msg := <-msgChan:
			p.procMessage(msg)
			if wait := len(msgChan); wait > cap(msgChan)/10*5 && wait%100 == 0 {
				p.Logger.Warnf("mult processor msgChan process,waitchan:%d/%d.", wait, cap(msgChan))
			}
		case <-waitD:
			return
		}
	}
}

func recoverFunc(p *Processor, pf ProcFunction) {
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
	}()
	pf()
}

func recoverEventCallback(p *Processor, ec EventCallback, ctx context.Context, event *Event) {
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
	}()
	msgName := fmt.Sprintf("EventID:%d", event.ID)
	proxyTrace := trace.TraceStart("Processor", msgName, true)
	defer trace.TraceFinishWithExpvar(proxyTrace, func(req *expvar.Map, time int64) {
		req.AddMessage(msgName, 1, time)
	})
	ec(ctx, event)
}
