package htlcswitch

import (
	"bytes"
	"container/list"
	"errors"
	"sync"
	"time"

	"github.com/decred/dcrlnd/clock"
	"github.com/decred/dcrlnd/lnwire"
)

// ErrMailBoxShuttingDown is returned when the mailbox is interrupted by a
// shutdown request.
var ErrMailBoxShuttingDown = errors.New("mailbox is shutting down")

// MailBox is an interface which represents a concurrent-safe, in-order
// delivery queue for messages from the network and also from the main switch.
// This struct servers as a buffer between incoming messages, and messages to
// the handled by the link. Each of the mutating methods within this interface
// should be implemented in a non-blocking manner.
type MailBox interface {
	// AddMessage appends a new message to the end of the message queue.
	AddMessage(msg lnwire.Message) error

	// AddPacket appends a new message to the end of the packet queue.
	AddPacket(pkt *htlcPacket) error

	// HasPacket queries the packets for a circuit key, this is used to drop
	// packets bound for the switch that already have a queued response.
	HasPacket(CircuitKey) bool

	// AckPacket removes a packet from the mailboxes in-memory replay
	// buffer. This will prevent a packet from being delivered after a link
	// restarts if the switch has remained online. The returned boolean
	// indicates whether or not a packet with the passed incoming circuit
	// key was removed.
	AckPacket(CircuitKey) bool

	// FailAdd fails an UpdateAddHTLC that exists within the mailbox,
	// removing it from the in-memory replay buffer. This will prevent the
	// packet from being delivered after the link restarts if the switch has
	// remained online. The generated LinkError will show an
	// OutgoingFailureDownstreamHtlcAdd FailureDetail.
	FailAdd(pkt *htlcPacket)

	// MessageOutBox returns a channel that any new messages ready for
	// delivery will be sent on.
	MessageOutBox() chan lnwire.Message

	// PacketOutBox returns a channel that any new packets ready for
	// delivery will be sent on.
	PacketOutBox() chan *htlcPacket

	// Clears any pending wire messages from the inbox.
	ResetMessages() error

	// Reset the packet head to point at the first element in the list.
	ResetPackets() error

	// Start starts the mailbox and any goroutines it needs to operate
	// properly.
	Start()

	// Stop signals the mailbox and its goroutines for a graceful shutdown.
	Stop()
}

type mailBoxConfig struct {
	// shortChanID is the short channel id of the channel this mailbox
	// belongs to.
	shortChanID lnwire.ShortChannelID

	// fetchUpdate retreives the most recent channel update for the channel
	// this mailbox belongs to.
	fetchUpdate func(lnwire.ShortChannelID) (*lnwire.ChannelUpdate, error)

	// forwardPackets send a varidic number of htlcPackets to the switch to
	// be routed. A quit channel should be provided so that the call can
	// properly exit during shutdown.
	forwardPackets func(chan struct{}, ...*htlcPacket) chan error

	// clock is a time source for the mailbox.
	clock clock.Clock

	// expiry is the interval after which Adds will be cancelled if they
	// have not been yet been delivered. The computed deadline will expiry
	// this long after the Adds are added via AddPacket.
	expiry time.Duration
}

// memoryMailBox is an implementation of the MailBox struct backed by purely
// in-memory queues.
type memoryMailBox struct {
	started sync.Once
	stopped sync.Once

	cfg *mailBoxConfig

	wireMessages *list.List
	wireMtx      sync.Mutex
	wireCond     *sync.Cond

	messageOutbox chan lnwire.Message
	msgReset      chan chan struct{}

	htlcPkts *list.List
	pktIndex map[CircuitKey]*list.Element
	pktHead  *list.Element
	pktMtx   sync.Mutex
	pktCond  *sync.Cond

	pktOutbox chan *htlcPacket
	pktReset  chan chan struct{}

	wireShutdown chan struct{}
	pktShutdown  chan struct{}
	quit         chan struct{}
}

// newMemoryMailBox creates a new instance of the memoryMailBox.
func newMemoryMailBox(cfg *mailBoxConfig) *memoryMailBox {
	box := &memoryMailBox{
		cfg:           cfg,
		wireMessages:  list.New(),
		htlcPkts:      list.New(),
		messageOutbox: make(chan lnwire.Message),
		pktOutbox:     make(chan *htlcPacket),
		msgReset:      make(chan chan struct{}, 1),
		pktReset:      make(chan chan struct{}, 1),
		pktIndex:      make(map[CircuitKey]*list.Element),
		wireShutdown:  make(chan struct{}),
		pktShutdown:   make(chan struct{}),
		quit:          make(chan struct{}),
	}
	box.wireCond = sync.NewCond(&box.wireMtx)
	box.pktCond = sync.NewCond(&box.pktMtx)

	return box
}

// A compile time assertion to ensure that memoryMailBox meets the MailBox
// interface.
var _ MailBox = (*memoryMailBox)(nil)

// courierType is an enum that reflects the distinct types of messages a
// MailBox can handle. Each type will be placed in an isolated mail box and
// will have a dedicated goroutine for delivering the messages.
type courierType uint8

const (
	// wireCourier is a type of courier that handles wire messages.
	wireCourier courierType = iota

	// pktCourier is a type of courier that handles htlc packets.
	pktCourier
)

// Start starts the mailbox and any goroutines it needs to operate properly.
//
// NOTE: This method is part of the MailBox interface.
func (m *memoryMailBox) Start() {
	m.started.Do(func() {
		go m.mailCourier(wireCourier)
		go m.mailCourier(pktCourier)
	})
}

// ResetMessages blocks until all buffered wire messages are cleared.
func (m *memoryMailBox) ResetMessages() error {
	msgDone := make(chan struct{})
	select {
	case m.msgReset <- msgDone:
		return m.signalUntilReset(wireCourier, msgDone)
	case <-m.quit:
		return ErrMailBoxShuttingDown
	}
}

// ResetPackets blocks until the head of packets buffer is reset, causing the
// packets to be redelivered in order.
func (m *memoryMailBox) ResetPackets() error {
	pktDone := make(chan struct{})
	select {
	case m.pktReset <- pktDone:
		return m.signalUntilReset(pktCourier, pktDone)
	case <-m.quit:
		return ErrMailBoxShuttingDown
	}
}

// signalUntilReset strobes the condition variable for the specified inbox type
// until receiving a response that the mailbox has processed a reset.
func (m *memoryMailBox) signalUntilReset(cType courierType,
	done chan struct{}) error {

	for {

		switch cType {
		case wireCourier:
			m.wireCond.Signal()
		case pktCourier:
			m.pktCond.Signal()
		}

		select {
		case <-time.After(time.Millisecond):
			continue
		case <-done:
			return nil
		case <-m.quit:
			return ErrMailBoxShuttingDown
		}
	}
}

// AckPacket removes the packet identified by it's incoming circuit key from the
// queue of packets to be delivered. The returned boolean indicates whether or
// not a packet with the passed incoming circuit key was removed.
//
// NOTE: It is safe to call this method multiple times for the same circuit key.
func (m *memoryMailBox) AckPacket(inKey CircuitKey) bool {
	m.pktCond.L.Lock()
	entry, ok := m.pktIndex[inKey]
	if !ok {
		m.pktCond.L.Unlock()
		return false
	}

	// Check whether we are removing the head of the queue. If so, we must
	// advance the head to the next packet before removing. It's possible
	// that the courier has already adanced the pktHead, so this check
	// prevents the pktHead from getting desynchronized.
	if entry == m.pktHead {
		m.pktHead = entry.Next()
	}
	m.htlcPkts.Remove(entry)
	delete(m.pktIndex, inKey)
	m.pktCond.L.Unlock()

	return true
}

// HasPacket queries the packets for a circuit key, this is used to drop packets
// bound for the switch that already have a queued response.
func (m *memoryMailBox) HasPacket(inKey CircuitKey) bool {
	m.pktCond.L.Lock()
	_, ok := m.pktIndex[inKey]
	m.pktCond.L.Unlock()

	return ok
}

// Stop signals the mailbox and its goroutines for a graceful shutdown.
//
// NOTE: This method is part of the MailBox interface.
func (m *memoryMailBox) Stop() {
	m.stopped.Do(func() {
		close(m.quit)

		m.signalUntilShutdown(wireCourier)
		m.signalUntilShutdown(pktCourier)
	})
}

// signalUntilShutdown strobes the condition variable of the passed courier
// type, blocking until the worker has exited.
func (m *memoryMailBox) signalUntilShutdown(cType courierType) {
	var (
		cond     *sync.Cond
		shutdown chan struct{}
	)

	switch cType {
	case wireCourier:
		cond = m.wireCond
		shutdown = m.wireShutdown
	case pktCourier:
		cond = m.pktCond
		shutdown = m.pktShutdown
	}

	for {
		select {
		case <-time.After(time.Millisecond):
			cond.Signal()
		case <-shutdown:
			return
		}
	}
}

// mailCourier is a dedicated goroutine whose job is to reliably deliver
// messages of a particular type. There are two types of couriers: wire
// couriers, and mail couriers. Depending on the passed courierType, this
// goroutine will assume one of two roles.
func (m *memoryMailBox) mailCourier(cType courierType) {
	switch cType {
	case wireCourier:
		defer close(m.wireShutdown)
	case pktCourier:
		defer close(m.pktShutdown)
	}

	// TODO(roasbeef): refactor...

	for {
		// First, we'll check our condition. If our target mailbox is
		// empty, then we'll wait until a new item is added.
		switch cType {
		case wireCourier:
			m.wireCond.L.Lock()
			for m.wireMessages.Front() == nil {
				m.wireCond.Wait()

				select {
				case msgDone := <-m.msgReset:
					m.wireMessages.Init()

					close(msgDone)
				case <-m.quit:
					m.wireCond.L.Unlock()
					return
				default:
				}
			}

		case pktCourier:
			m.pktCond.L.Lock()
			for m.pktHead == nil {
				m.pktCond.Wait()

				select {
				// Resetting the packet queue means just moving
				// our pointer to the front. This ensures that
				// any un-ACK'd messages are re-delivered upon
				// reconnect.
				case pktDone := <-m.pktReset:
					m.pktHead = m.htlcPkts.Front()

					close(pktDone)
				case <-m.quit:
					m.pktCond.L.Unlock()
					return
				default:
				}
			}
		}

		var (
			nextPkt   *htlcPacket
			nextPktEl *list.Element
			nextMsg   lnwire.Message
		)
		switch cType {
		// Grab the datum off the front of the queue, shifting the
		// slice's reference down one in order to remove the datum from
		// the queue.
		case wireCourier:
			entry := m.wireMessages.Front()
			nextMsg = m.wireMessages.Remove(entry).(lnwire.Message)

		// For packets, we actually never remove an item until it has
		// been ACK'd by the link. This ensures that if a read packet
		// doesn't make it into a commitment, then it'll be
		// re-delivered once the link comes back online.
		case pktCourier:
			nextPkt = m.pktHead.Value.(*htlcPacket)
			nextPktEl = m.pktHead
		}

		// Now that we're done with the condition, we can unlock it to
		// allow any callers to append to the end of our target queue.
		switch cType {
		case wireCourier:
			m.wireCond.L.Unlock()
		case pktCourier:
			m.pktCond.L.Unlock()
		}

		// With the next message obtained, we'll now select to attempt
		// to deliver the message. If we receive a kill signal, then
		// we'll bail out.
		switch cType {
		case wireCourier:
			select {
			case m.messageOutbox <- nextMsg:
			case msgDone := <-m.msgReset:
				m.wireCond.L.Lock()
				m.wireMessages.Init()
				m.wireCond.L.Unlock()

				close(msgDone)
			case <-m.quit:
				return
			}

		case pktCourier:
			select {
			case m.pktOutbox <- nextPkt:
				m.pktCond.L.Lock()
				// Only advance the pktHead if this packet
				// is still at the head of the queue.
				if m.pktHead != nil && m.pktHead == nextPktEl {
					m.pktHead = m.pktHead.Next()
				}
				m.pktCond.L.Unlock()

			case pktDone := <-m.pktReset:
				m.pktCond.L.Lock()
				m.pktHead = m.htlcPkts.Front()
				m.pktCond.L.Unlock()

				close(pktDone)
			case <-m.quit:
				return
			}
		}

	}
}

// AddMessage appends a new message to the end of the message queue.
//
// NOTE: This method is safe for concrete use and part of the MailBox
// interface.
func (m *memoryMailBox) AddMessage(msg lnwire.Message) error {
	// First, we'll lock the condition, and add the message to the end of
	// the wire message inbox.
	m.wireCond.L.Lock()
	m.wireMessages.PushBack(msg)
	m.wireCond.L.Unlock()

	// With the message added, we signal to the mailCourier that there are
	// additional messages to deliver.
	m.wireCond.Signal()

	return nil
}

// AddPacket appends a new message to the end of the packet queue.
//
// NOTE: This method is safe for concrete use and part of the MailBox
// interface.
func (m *memoryMailBox) AddPacket(pkt *htlcPacket) error {
	// First, we'll lock the condition, and add the packet to the end of
	// the htlc packet inbox.
	m.pktCond.L.Lock()
	if _, ok := m.pktIndex[pkt.inKey()]; ok {
		m.pktCond.L.Unlock()
		return nil
	}

	entry := m.htlcPkts.PushBack(pkt)
	m.pktIndex[pkt.inKey()] = entry
	if m.pktHead == nil {
		m.pktHead = entry
	}
	m.pktCond.L.Unlock()

	// With the packet added, we signal to the mailCourier that there are
	// additional packets to consume.
	m.pktCond.Signal()

	return nil
}

// FailAdd fails an UpdateAddHTLC that exists within the mailbox, removing it
// from the in-memory replay buffer. This will prevent the packet from being
// delivered after the link restarts if the switch has remained online. The
// generated LinkError will show an OutgoingFailureDownstreamHtlcAdd
// FailureDetail.
func (m *memoryMailBox) FailAdd(pkt *htlcPacket) {
	// First, remove the packet from mailbox. If we didn't find the packet
	// because it has already been acked, we'll exit early to avoid sending
	// a duplicate fail message through the switch.
	if !m.AckPacket(pkt.inKey()) {
		return
	}

	var (
		localFailure = false
		reason       lnwire.OpaqueReason
	)

	// Create a temporary channel failure which we will send back to our
	// peer if this is a forward, or report to the user if the failed
	// payment was locally initiated.
	var failure lnwire.FailureMessage
	update, err := m.cfg.fetchUpdate(m.cfg.shortChanID)
	if err != nil {
		failure = &lnwire.FailTemporaryNodeFailure{}
	} else {
		failure = lnwire.NewTemporaryChannelFailure(update)
	}

	// If the payment was locally initiated (which is indicated by a nil
	// obfuscator), we do not need to encrypt it back to the sender.
	if pkt.obfuscator == nil {
		var b bytes.Buffer
		err := lnwire.EncodeFailure(&b, failure, 0)
		if err != nil {
			log.Errorf("Unable to encode failure: %v", err)
			return
		}
		reason = lnwire.OpaqueReason(b.Bytes())
		localFailure = true
	} else {
		// If the packet is part of a forward, (identified by a non-nil
		// obfuscator) we need to encrypt the error back to the source.
		var err error
		reason, err = pkt.obfuscator.EncryptFirstHop(failure)
		if err != nil {
			log.Errorf("Unable to obfuscate error: %v", err)
			return
		}
	}

	// Create a link error containing the temporary channel failure and a
	// detail which indicates the we failed to add the htlc.
	linkError := NewDetailedLinkError(
		failure, OutgoingFailureDownstreamHtlcAdd,
	)

	failPkt := &htlcPacket{
		incomingChanID: pkt.incomingChanID,
		incomingHTLCID: pkt.incomingHTLCID,
		circuit:        pkt.circuit,
		sourceRef:      pkt.sourceRef,
		hasSource:      true,
		localFailure:   localFailure,
		linkFailure:    linkError,
		htlc: &lnwire.UpdateFailHTLC{
			Reason: reason,
		},
	}

	errChan := m.cfg.forwardPackets(m.quit, failPkt)
	go handleBatchFwdErrs(errChan, log)
}

// MessageOutBox returns a channel that any new messages ready for delivery
// will be sent on.
//
// NOTE: This method is part of the MailBox interface.
func (m *memoryMailBox) MessageOutBox() chan lnwire.Message {
	return m.messageOutbox
}

// PacketOutBox returns a channel that any new packets ready for delivery will
// be sent on.
//
// NOTE: This method is part of the MailBox interface.
func (m *memoryMailBox) PacketOutBox() chan *htlcPacket {
	return m.pktOutbox
}

// mailOrchestrator is responsible for coordinating the creation and lifecycle
// of mailboxes used within the switch. It supports the ability to create
// mailboxes, reassign their short channel id's, deliver htlc packets, and
// queue packets for mailboxes that have not been created due to a link's late
// registration.
type mailOrchestrator struct {
	mu sync.RWMutex

	cfg *mailOrchConfig

	// mailboxes caches exactly one mailbox for all known channels.
	mailboxes map[lnwire.ChannelID]MailBox

	// liveIndex maps a live short chan id to the primary mailbox key.
	// An index in liveIndex map is only entered under two conditions:
	//   1. A link has a non-zero short channel id at time of AddLink.
	//   2. A link receives a non-zero short channel via UpdateShortChanID.
	liveIndex map[lnwire.ShortChannelID]lnwire.ChannelID

	// TODO(conner): add another pair of indexes:
	//   chan_id -> short_chan_id
	//   short_chan_id -> mailbox
	// so that Deliver can lookup mailbox directly once live,
	// but still queriable by channel_id.

	// unclaimedPackets maps a live short chan id to queue of packets if no
	// mailbox has been created.
	unclaimedPackets map[lnwire.ShortChannelID][]*htlcPacket
}

type mailOrchConfig struct {
	// forwardPackets send a varidic number of htlcPackets to the switch to
	// be routed. A quit channel should be provided so that the call can
	// properly exit during shutdown.
	forwardPackets func(chan struct{}, ...*htlcPacket) chan error

	// fetchUpdate retreives the most recent channel update for the channel
	// this mailbox belongs to.
	fetchUpdate func(lnwire.ShortChannelID) (*lnwire.ChannelUpdate, error)

	// clock is a time source for the generated mailboxes.
	clock clock.Clock

	// expiry is the interval after which Adds will be cancelled if they
	// have not been yet been delivered. The computed deadline will expiry
	// this long after the Adds are added to a mailbox via AddPacket.
	expiry time.Duration
}

// newMailOrchestrator initializes a fresh mailOrchestrator.
func newMailOrchestrator(cfg *mailOrchConfig) *mailOrchestrator {
	return &mailOrchestrator{
		cfg:              cfg,
		mailboxes:        make(map[lnwire.ChannelID]MailBox),
		liveIndex:        make(map[lnwire.ShortChannelID]lnwire.ChannelID),
		unclaimedPackets: make(map[lnwire.ShortChannelID][]*htlcPacket),
	}
}

// Stop instructs the orchestrator to stop all active mailboxes.
func (mo *mailOrchestrator) Stop() {
	for _, mailbox := range mo.mailboxes {
		mailbox.Stop()
	}
}

// GetOrCreateMailBox returns an existing mailbox belonging to `chanID`, or
// creates and returns a new mailbox if none is found.
func (mo *mailOrchestrator) GetOrCreateMailBox(chanID lnwire.ChannelID,
	shortChanID lnwire.ShortChannelID) MailBox {

	// First, try lookup the mailbox directly using only the shared mutex.
	mo.mu.RLock()
	mailbox, ok := mo.mailboxes[chanID]
	if ok {
		mo.mu.RUnlock()
		return mailbox
	}
	mo.mu.RUnlock()

	// Otherwise, we will try again with exclusive lock, creating a mailbox
	// if one still has not been created.
	mo.mu.Lock()
	mailbox = mo.exclusiveGetOrCreateMailBox(chanID, shortChanID)
	mo.mu.Unlock()

	return mailbox
}

// exclusiveGetOrCreateMailBox checks for the existence of a mailbox for the
// given channel id. If none is found, a new one is creates, started, and
// recorded.
//
// NOTE: This method MUST be invoked with the mailOrchestrator's exclusive lock.
func (mo *mailOrchestrator) exclusiveGetOrCreateMailBox(
	chanID lnwire.ChannelID, shortChanID lnwire.ShortChannelID) MailBox {

	mailbox, ok := mo.mailboxes[chanID]
	if !ok {
		mailbox = newMemoryMailBox(&mailBoxConfig{
			shortChanID:    shortChanID,
			fetchUpdate:    mo.cfg.fetchUpdate,
			forwardPackets: mo.cfg.forwardPackets,
			clock:          mo.cfg.clock,
			expiry:         mo.cfg.expiry,
		})
		mailbox.Start()
		mo.mailboxes[chanID] = mailbox
	}

	return mailbox
}

// BindLiveShortChanID registers that messages bound for a particular short
// channel id should be forwarded to the mailbox corresponding to the given
// channel id. This method also checks to see if there are any unclaimed
// packets for this short_chan_id. If any are found, they are delivered to the
// mailbox and removed (marked as claimed).
func (mo *mailOrchestrator) BindLiveShortChanID(mailbox MailBox,
	cid lnwire.ChannelID, sid lnwire.ShortChannelID) {

	mo.mu.Lock()
	// Update the mapping from short channel id to mailbox's channel id.
	mo.liveIndex[sid] = cid

	// Retrieve any unclaimed packets destined for this mailbox.
	pkts := mo.unclaimedPackets[sid]
	delete(mo.unclaimedPackets, sid)
	mo.mu.Unlock()

	// Deliver the unclaimed packets.
	for _, pkt := range pkts {
		mailbox.AddPacket(pkt)
	}
}

// Deliver lookups the target mailbox using the live index from short_chan_id
// to channel_id. If the mailbox is found, the message is delivered directly.
// Otherwise the packet is recorded as unclaimed, and will be delivered to the
// mailbox upon the subsequent call to BindLiveShortChanID.
func (mo *mailOrchestrator) Deliver(
	sid lnwire.ShortChannelID, pkt *htlcPacket) error {

	var (
		mailbox MailBox
		found   bool
	)

	// First, try to find the channel id for the target short_chan_id. If
	// the link is live, we will also look up the created mailbox.
	mo.mu.RLock()
	chanID, isLive := mo.liveIndex[sid]
	if isLive {
		mailbox, found = mo.mailboxes[chanID]
	}
	mo.mu.RUnlock()

	// The link is live and target mailbox was found, deliver immediately.
	if isLive && found {
		return mailbox.AddPacket(pkt)
	}

	// If we detected that the link has not been made live, we will acquire
	// the exclusive lock preemptively in order to queue this packet in the
	// list of unclaimed packets.
	mo.mu.Lock()

	// Double check to see if the mailbox has been not made live since the
	// release of the shared lock.
	//
	// NOTE: Checking again with the exclusive lock held prevents a race
	// condition where BindLiveShortChanID is interleaved between the
	// release of the shared lock, and acquiring the exclusive lock. The
	// result would be stuck packets, as they wouldn't be redelivered until
	// the next call to BindLiveShortChanID, which is expected to occur
	// infrequently.
	chanID, isLive = mo.liveIndex[sid]
	if isLive {
		// Reaching this point indicates the mailbox is actually live.
		// We'll try to load the mailbox using the fresh channel id.
		//
		// NOTE: This should never create a new mailbox, as the live
		// index should only be set if the mailbox had been initialized
		// beforehand.  However, this does ensure that this case is
		// handled properly in the event that it could happen.
		mailbox = mo.exclusiveGetOrCreateMailBox(chanID, sid)
		mo.mu.Unlock()

		// Deliver the packet to the mailbox if it was found or created.
		return mailbox.AddPacket(pkt)
	}

	// Finally, if the channel id is still not found in the live index,
	// we'll add this to the list of unclaimed packets. These will be
	// delivered upon the next call to BindLiveShortChanID.
	mo.unclaimedPackets[sid] = append(mo.unclaimedPackets[sid], pkt)
	mo.mu.Unlock()

	return nil
}
