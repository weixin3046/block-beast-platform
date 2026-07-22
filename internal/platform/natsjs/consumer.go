package natsjs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/nats-io/nats.go"
)

const deadLetterStreamName = "BLOCK_BEAST_DEAD_LETTERS"

// Handler 处理一条领域事件；返回错误时消息将按退避策略重投，
// 超过最大投递次数后进入死信流。
type Handler func(ctx context.Context, event events.Event) error

// ConsumerStats 是消费者监控计数器的快照。
type ConsumerStats struct {
	Received     int64
	Processed    int64
	Retried      int64
	DeadLettered int64
}

// ConsumerConfig 配置消费者的重试、死信与日志行为。
type ConsumerConfig struct {
	// MaxDeliver 是单条消息的最大投递次数（含首次），超过后进入死信流。
	MaxDeliver int
	// Backoff 是第 N 次重投前的等待时间，下标越界时沿用最后一个值。
	Backoff []time.Duration
	// AckWait 是 JetStream 等待确认的时长，必须大于最大退避时间。
	AckWait time.Duration
	Logger  *slog.Logger
}

func (config ConsumerConfig) withDefaults() ConsumerConfig {
	if config.MaxDeliver <= 0 {
		config.MaxDeliver = 5
	}
	if len(config.Backoff) == 0 {
		config.Backoff = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	}
	if config.AckWait <= 0 {
		config.AckWait = 60 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return config
}

// Consumer 是基于 JetStream 耐用推送消费者的领域事件处理器。
type Consumer struct {
	connection   *nats.Conn
	jetStream    nats.JetStreamContext
	config       ConsumerConfig
	logger       *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	subs         []*nats.Subscription
	received     atomic.Int64
	processed    atomic.Int64
	retried      atomic.Int64
	deadLettered atomic.Int64
}

// NewConsumer 建立 NATS 连接并确保死信流存在。
func NewConsumer(url string, config ConsumerConfig) (*Consumer, error) {
	config = config.withDefaults()
	connection, err := nats.Connect(url)
	if err != nil {
		return nil, err
	}
	jetStream, err := connection.JetStream()
	if err != nil {
		connection.Close()
		return nil, err
	}
	if _, err := jetStream.StreamInfo(deadLetterStreamName); errors.Is(err, nats.ErrStreamNotFound) {
		_, err = jetStream.AddStream(&nats.StreamConfig{
			Name:     deadLetterStreamName,
			Subjects: []string{"deadletter.>"},
		})
	}
	if err != nil {
		connection.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Consumer{
		connection: connection,
		jetStream:  jetStream,
		config:     config,
		logger:     config.Logger,
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// Subscribe 以耐用消费者订阅主题；队列名与耐用名一致，便于后续多实例负载均衡。
func (consumer *Consumer) Subscribe(subject string, durable string, handler Handler) error {
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	subscription, err := consumer.jetStream.QueueSubscribe(subject, durable, func(msg *nats.Msg) {
		consumer.handle(subject, handler, msg)
	},
		nats.Durable(durable),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.MaxDeliver(consumer.config.MaxDeliver),
		nats.AckWait(consumer.config.AckWait),
	)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	consumer.subs = append(consumer.subs, subscription)
	consumer.logger.Info("consumer subscribed", "subject", subject, "durable", durable, "max_deliver", consumer.config.MaxDeliver)
	return nil
}

// Stats 返回当前监控计数器快照。
func (consumer *Consumer) Stats() ConsumerStats {
	return ConsumerStats{
		Received:     consumer.received.Load(),
		Processed:    consumer.processed.Load(),
		Retried:      consumer.retried.Load(),
		DeadLettered: consumer.deadLettered.Load(),
	}
}

// Close 取消订阅并关闭连接；耐用消费者保留在服务器上，重启后从断点继续。
func (consumer *Consumer) Close() {
	consumer.cancel()
	consumer.mu.Lock()
	subs := append([]*nats.Subscription(nil), consumer.subs...)
	consumer.mu.Unlock()
	for _, subscription := range subs {
		_ = subscription.Unsubscribe()
	}
	consumer.connection.Close()
}

func (consumer *Consumer) handle(subject string, handler Handler, msg *nats.Msg) {
	consumer.received.Add(1)
	deliveries := 1
	metadata, err := msg.Metadata()
	if err != nil {
		consumer.logger.Warn("message metadata unavailable, assuming first delivery", "subject", subject, "error", err)
	} else {
		deliveries = int(metadata.NumDelivered)
	}
	event := events.Event{
		ID:      msg.Header.Get(nats.MsgIdHdr),
		Type:    msg.Subject,
		Payload: msg.Data,
	}
	handlerErr := handler(consumer.ctx, event)
	switch decide(deliveries, consumer.config.MaxDeliver, consumer.config.Backoff, handlerErr).kind {
	case dispositionAck:
		if err := msg.Ack(); err != nil {
			consumer.logger.Error("ack failed", "subject", subject, "event_id", event.ID, "error", err)
			return
		}
		consumer.processed.Add(1)
	case dispositionRetry:
		delay := backoffFor(deliveries, consumer.config.Backoff)
		if err := msg.NakWithDelay(delay); err != nil {
			consumer.logger.Error("nak failed", "subject", subject, "event_id", event.ID, "error", err)
			return
		}
		consumer.retried.Add(1)
		consumer.logger.Warn("event handler failed, will retry",
			"subject", subject, "event_id", event.ID, "delivery", deliveries, "retry_in", delay, "error", handlerErr)
	case dispositionDeadLetter:
		if err := consumer.deadLetter(msg, event, deliveries, handlerErr); err != nil {
			consumer.logger.Error("dead-letter publish failed, message stays for redelivery", "subject", subject, "event_id", event.ID, "error", err)
			return
		}
		if err := msg.Term(); err != nil {
			consumer.logger.Error("term failed", "subject", subject, "event_id", event.ID, "error", err)
		}
		consumer.deadLettered.Add(1)
		consumer.logger.Error("event dead-lettered after max deliveries",
			"subject", subject, "event_id", event.ID, "deliveries", deliveries, "error", handlerErr)
	}
}

// deadLetter 将原始消息连同失败上下文发布到死信流。
func (consumer *Consumer) deadLetter(msg *nats.Msg, event events.Event, deliveries int, handlerErr error) error {
	deadMsg := nats.NewMsg("deadletter." + msg.Subject)
	deadMsg.Data = msg.Data
	deadMsg.Header.Set("X-Event-Id", event.ID)
	deadMsg.Header.Set("X-Original-Subject", msg.Subject)
	deadMsg.Header.Set("X-Delivery-Count", strconv.Itoa(deliveries))
	reason := "unknown"
	if handlerErr != nil {
		reason = handlerErr.Error()
	}
	deadMsg.Header.Set("X-Failure-Reason", reason)
	_, err := consumer.jetStream.PublishMsg(deadMsg, nats.MsgId(event.ID))
	return err
}

type dispositionKind int

const (
	dispositionAck dispositionKind = iota
	dispositionRetry
	dispositionDeadLetter
)

type disposition struct {
	kind dispositionKind
}

// decide 根据投递次数与处理结果决定消息的处置方式：成功确认、退避重试或死信。
func decide(deliveries int, maxDeliver int, _ []time.Duration, handlerErr error) disposition {
	if handlerErr == nil {
		return disposition{kind: dispositionAck}
	}
	if deliveries >= maxDeliver {
		return disposition{kind: dispositionDeadLetter}
	}
	return disposition{kind: dispositionRetry}
}

// backoffFor 返回第 N 次投递失败后的重投等待时间。
func backoffFor(deliveries int, backoff []time.Duration) time.Duration {
	if len(backoff) == 0 {
		return time.Second
	}
	index := deliveries - 1
	if index < 0 {
		index = 0
	}
	if index >= len(backoff) {
		index = len(backoff) - 1
	}
	return backoff[index]
}
