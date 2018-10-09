package network

import (
	"context"
	"fmt"
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
	//  立即回调消息，如果想要线程安全，必须设置为false，默认为false
	immediateMode bool
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
		messageChan:   make(chan *Message, 10000),
		eventChan:     make(chan *Event, 10000),
		funcChan:      make(chan ProcFunction, 10000),
		eventCallback: make(map[int32]EventCallback),
		callbackMap:   make(map[int32]MsgCallback),
		loopTime:      time,
		done:          chanutil.NewDoneChan(),
		Logger:        logger,
	}
	return p
}
func (p *Processor) IsClosed() bool {
	return p.done.R().Done()
}
func (p *Processor) Close() {
	p.done.SetDone()
}
func (p *Processor) GetCallbackIds() []int32 {
	list := make([]int32, 0, len(p.callbackMap))
	for id := range p.callbackMap {
		list = append(list, id)
	}
	return list
}

func (p *Processor) AddCallback(id int32, callback MsgCallback) {
	if id != 0 {
		p.callbackMap[id] = callback
	}
}

func (p *Processor) GetCallback(id int32) MsgCallback {
	return p.callbackMap[id]
}

func (p *Processor) SetTickFunc(uptime time.Duration, upcall ProcFunction) {
	p.loopTime = uptime
	p.updateCallback = upcall
}

func (p *Processor) RemoveCallback(id int32) {
	delete(p.callbackMap, id)
}

func (p *Processor) AddEventCallback(id int32, callback EventCallback) {
	p.eventCallback[id] = callback
}

func (p *Processor) RemoveEventCallback(id int32) {
	delete(p.eventCallback, id)
}
func (p *Processor) AddFunc(funz ProcFunction) bool {
	select {
	case <-p.done:
		return false
	default:
		select {
		case <-p.done:
			return false
		case p.funcChan <- funz:
			if wait := len(p.funcChan); wait > cap(p.funcChan)/10*6 && wait%100 == 0 {
				p.Logger.Warnf("processor funcChan process,waitchan:%d/%d.", wait, cap(p.funcChan))
			}
			return true
		}
	}
}

func (p *Processor) TriggerEvent(event *Event) bool {
	select {
	case <-p.done:
		return false
	default:
		select {
		case <-p.done:
			return false
		case p.eventChan <- event:
			if wait := len(p.eventChan); wait > cap(p.eventChan)/10*6 && wait%100 == 0 {
				p.Logger.Warnf("processor eventChan process,waitchan:%d/%d.", wait, cap(p.eventChan))
			}
			return true
		}
	}
}

func (p *Processor) AddMessage(msg *Message) bool {
	select {
	case <-p.done:
		return false
	default:
		select {
		case <-p.done:
			return false
		case p.messageChan <- msg:
			if wait := len(p.messageChan); wait > cap(p.messageChan)/10*6 && wait%100 == 0 {
				p.Logger.Warnf("processor messageChan process,waitchan:%d/%d.", wait, cap(p.messageChan))
			}
			return true
		}
	}
}
func (p *Processor) ProcMessage(msg *Message) {
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
	}()
	msgName := fmt.Sprintf("MsgID:%d", msg.Head.ID)
	proxyTrace := trace.TraceStart("Processor.ProcMessage", msgName)
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

func (p *Processor) ImmediateMode(state bool) {
	p.immediateMode = state
}

func (p *Processor) IsImmediate() bool {
	return p.immediateMode
}

func (p *Processor) UnHandledHandler(handle MsgCallback) {
	p.unHandledHandler = handle
}

// StartProcess 开始处理信息
// 只有调用了这个接口，处理器才会处理实际的信息，以及实际发送消息
func (p *Processor) StartProcess(ctx context.Context, wg *sync.WaitGroup, loopFun func(ProcessType, interface{})) {
	wg.Add(1)
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
		p.done.SetDone()
		wg.Done()
		p.Logger.Infof("processor is stoped.")
	}()
	p.Logger.Infof("processor is starting.")

	proxyTrace := trace.TraceStart("Goroutine", "Processor")
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
			p.ProcMessage(msg)
		case event := <-p.eventChan:
			if loopFun != nil {
				loopFun(ProcessTypeEvent, event)
			}
			if event.ID == ExitEvent {
				p.Logger.Infof("processor exit:%v.", event.Param)
				return
			}
			if cb, ok := p.eventCallback[event.ID]; ok {
				RecoverEventCallback(p, cb, ctx, event)
				// } else {
				// 	p.Logger.Warnf("can't find event callback(%d)", event.ID)
			}
		case f := <-p.funcChan:
			if loopFun != nil {
				loopFun(ProcessTypeFunc, f)
			}
			RecoverFunc(p, f)
		case <-tick:
			if p.updateCallback != nil {
				if loopFun != nil {
					loopFun(ProcessTypeTick, p.updateCallback)
				}
				RecoverFunc(p, p.updateCallback)
			}
		}
	}
}

func RecoverFunc(p *Processor, pf ProcFunction) {
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
	}()
	pf()
}
func RecoverEventCallback(p *Processor, ec EventCallback, ctx context.Context, event *Event) {
	defer func() {
		if x := recover(); x != nil {
			p.Logger.Errorf("recover error:%v.", x)
		}
	}()
	msgName := fmt.Sprintf("EventID:%d", event.ID)
	proxyTrace := trace.TraceStart("Processor.EventCallback", msgName)
	defer trace.TraceFinishWithExpvar(proxyTrace, func(req *expvar.Map, time int64) {
		req.AddMessage(msgName, 1, time)
	})
	ec(ctx, event)
}
