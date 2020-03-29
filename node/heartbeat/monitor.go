//go:generate protoc -I=proto -I=$GOPATH/src -I=$GOPATH/src/github.com/gogo/protobuf/protobuf  --gogoslick_out=. heartbeat.proto
package heartbeat

import (
	"bytes"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ElrondNetwork/elrond-go/core"
	"github.com/ElrondNetwork/elrond-go/core/check"
	"github.com/ElrondNetwork/elrond-go/logger"
	"github.com/ElrondNetwork/elrond-go/marshal"
	"github.com/ElrondNetwork/elrond-go/p2p"
	"github.com/ElrondNetwork/elrond-go/statusHandler"
)

var log = logger.GetOrCreate("node/heartbeat")

const hardforkTriggerString = "hardfork trigger"

// ArgHeartbeatMonitor represents the arguments for the heartbeat monitor
type ArgHeartbeatMonitor struct {
	Marshalizer                 marshal.Marshalizer
	MaxDurationPeerUnresponsive time.Duration
	PubKeysMap                  map[uint32][]string
	GenesisTime                 time.Time
	MessageHandler              MessageHandler
	Storer                      HeartbeatStorageHandler
	PeerTypeProvider            PeerTypeProviderHandler
	Timer                       Timer
	AntifloodHandler            P2PAntifloodHandler
	HardforkTrigger             HardforkTrigger
}

// Monitor represents the heartbeat component that processes received heartbeat messages
type Monitor struct {
	maxDurationPeerUnresponsive time.Duration
	marshalizer                 marshal.Marshalizer
	peerTypeProvider            PeerTypeProviderHandler
	mutHeartbeatMessages        sync.RWMutex
	heartbeatMessages           map[string]*heartbeatMessageInfo
	mutPubKeysMap               sync.RWMutex
	pubKeysMap                  map[uint32][]string
	mutFullPeersSlice           sync.RWMutex
	fullPeersSlice              [][]byte
	appStatusHandler            core.AppStatusHandler
	genesisTime                 time.Time
	messageHandler              MessageHandler
	storer                      HeartbeatStorageHandler
	timer                       Timer
	antifloodHandler            P2PAntifloodHandler
	hardforkTrigger             HardforkTrigger
}

// NewMonitor returns a new monitor instance
func NewMonitor(arg ArgHeartbeatMonitor) (*Monitor, error) {
	if check.IfNil(arg.Marshalizer) {
		return nil, ErrNilMarshalizer
	}
	if check.IfNil(arg.PeerTypeProvider) {
		return nil, ErrNilPeerTypeProvider
	}
	if len(arg.PubKeysMap) == 0 {
		return nil, ErrEmptyPublicKeysMap
	}
	if check.IfNil(arg.MessageHandler) {
		return nil, ErrNilMessageHandler
	}
	if check.IfNil(arg.Storer) {
		return nil, ErrNilHeartbeatStorer
	}
	if check.IfNil(arg.Timer) {
		return nil, ErrNilTimer
	}
	if check.IfNil(arg.AntifloodHandler) {
		return nil, ErrNilAntifloodHandler
	}
	if check.IfNil(arg.HardforkTrigger) {
		return nil, ErrNilHardforkTrigger
	}

	mon := &Monitor{
		marshalizer:                 arg.Marshalizer,
		heartbeatMessages:           make(map[string]*heartbeatMessageInfo),
		peerTypeProvider:            arg.PeerTypeProvider,
		maxDurationPeerUnresponsive: arg.MaxDurationPeerUnresponsive,
		appStatusHandler:            &statusHandler.NilStatusHandler{},
		genesisTime:                 arg.GenesisTime,
		messageHandler:              arg.MessageHandler,
		storer:                      arg.Storer,
		timer:                       arg.Timer,
		antifloodHandler:            arg.AntifloodHandler,
		hardforkTrigger:             arg.HardforkTrigger,
	}

	err := mon.storer.UpdateGenesisTime(arg.GenesisTime)
	if err != nil {
		return nil, err
	}

	err = mon.initializeHeartbeatMessagesInfo(arg.PubKeysMap)
	if err != nil {
		return nil, err
	}

	err = mon.loadRestOfPubKeysFromStorage()
	if err != nil {
		log.Debug("heartbeat can't load public keys from storage", "error", err.Error())
	}

	return mon, nil
}

func (m *Monitor) initializeHeartbeatMessagesInfo(pubKeysMap map[uint32][]string) error {
	pubKeysMapCopy := make(map[uint32][]string)
	pubKeysToSave := make(map[string]*heartbeatMessageInfo)
	for shardId, pubKeys := range pubKeysMap {
		for _, pubkey := range pubKeys {
			e := m.initializeHeartBeatForPK(pubkey, shardId, pubKeysToSave, pubKeysMapCopy)
			if e != nil {
				return e
			}
		}
	}

	go m.SaveMultipleHeartbeatMessageInfos(pubKeysToSave)

	m.pubKeysMap = pubKeysMapCopy
	return nil
}

func (m *Monitor) initializeHeartBeatForPK(
	pubkey string,
	shardId uint32,
	pubKeysToSave map[string]*heartbeatMessageInfo,
	pubKeysMapCopy map[uint32][]string,
) error {
	hbmi, err := m.loadHbmiFromStorer(pubkey)
	if err != nil { // if pubKey not found in DB, create a new instance
		peerType := m.computePeerType([]byte(pubkey), shardId)
		hbmi, err = newHeartbeatMessageInfo(m.maxDurationPeerUnresponsive, peerType, m.genesisTime, m.timer)
		if err != nil {
			return err
		}

		hbmi.genesisTime = m.genesisTime
		hbmi.computedShardID = shardId
		pubKeysToSave[pubkey] = hbmi
	}
	m.heartbeatMessages[pubkey] = hbmi
	pubKeysMapCopy[shardId] = append(pubKeysMapCopy[shardId], pubkey)
	return nil
}

// SaveMultipleHeartbeatMessageInfos stores all heartbeatMessageInfos to the storer
func (m *Monitor) SaveMultipleHeartbeatMessageInfos(pubKeysToSave map[string]*heartbeatMessageInfo) {
	m.mutHeartbeatMessages.RLock()
	defer m.mutHeartbeatMessages.RUnlock()

	for key, hmbi := range pubKeysToSave {
		hbDTO := m.convertToExportedStruct(hmbi)
		err := m.storer.SavePubkeyData([]byte(key), &hbDTO)
		if err != nil {
			log.Debug("cannot save heartbeat to db", "error", err.Error())
		}
	}
}

func (m *Monitor) loadRestOfPubKeysFromStorage() error {
	peersSlice, err := m.storer.LoadKeys()
	if err != nil {
		return err
	}

	for _, peer := range peersSlice {
		pubKey := string(peer)
		_, ok := m.heartbeatMessages[pubKey]
		if !ok { // peer not in nodes map
			hbmi, err1 := m.loadHbmiFromStorer(pubKey)
			if err1 != nil {
				continue
			}
			m.heartbeatMessages[pubKey] = hbmi
		}
	}

	return nil
}

func (m *Monitor) loadHbmiFromStorer(pubKey string) (*heartbeatMessageInfo, error) {
	hbmiDTO, err := m.storer.LoadHbmiDTO(pubKey)
	if err != nil {
		return nil, err
	}

	receivedHbmi := m.convertFromExportedStruct(*hbmiDTO, m.maxDurationPeerUnresponsive)
	receivedHbmi.getTimeHandler = m.timer.Now
	crtTime := m.timer.Now()
	crtDuration := crtTime.Sub(receivedHbmi.lastUptimeDowntime)
	crtDuration = maxDuration(0, crtDuration)
	if receivedHbmi.isActive {
		receivedHbmi.totalUpTime += crtDuration
		receivedHbmi.timeStamp = crtTime
	} else {
		receivedHbmi.totalDownTime += crtDuration
	}
	receivedHbmi.lastUptimeDowntime = crtTime
	receivedHbmi.genesisTime = m.genesisTime
	receivedHbmi.peerType = m.computePeerType([]byte(pubKey), receivedHbmi.computedShardID)

	return receivedHbmi, nil
}

// SetAppStatusHandler will set the AppStatusHandler which will be used for monitoring
func (m *Monitor) SetAppStatusHandler(ash core.AppStatusHandler) error {
	if ash == nil || ash.IsInterfaceNil() {
		return ErrNilAppStatusHandler
	}

	m.appStatusHandler = ash
	return nil
}

// ProcessReceivedMessage satisfies the p2p.MessageProcessor interface so it can be called
// by the p2p subsystem each time a new heartbeat message arrives
func (m *Monitor) ProcessReceivedMessage(message p2p.MessageP2P, fromConnectedPeer p2p.PeerID) error {
	if check.IfNil(message) {
		return ErrNilMessage
	}
	if message.Data() == nil {
		return ErrNilDataToProcess
	}

	err := m.antifloodHandler.CanProcessMessage(message, fromConnectedPeer)
	if err != nil {
		return err
	}
	err = m.antifloodHandler.CanProcessMessageOnTopic(fromConnectedPeer, core.HeartbeatTopic)
	if err != nil {
		return err
	}

	hbRecv, err := m.messageHandler.CreateHeartbeatFromP2PMessage(message)
	if err != nil {
		return err
	}

	isHardforkTrigger := bytes.Equal(hbRecv.Payload, []byte(hardforkTriggerString))
	if isHardforkTrigger {
		errHardforkTrigger := m.hardforkTrigger.TriggerReceived(hbRecv.Pubkey, hbRecv.Payload)
		if errHardforkTrigger != nil {
			return errHardforkTrigger
		}
	}

	//message is validated, process should be done async, method can return nil
	go m.addHeartbeatMessageToMap(hbRecv)

	go m.computeAllHeartbeatMessages()

	return nil
}

func (m *Monitor) addHeartbeatMessageToMap(hb *Heartbeat) {
	pubKeyStr := string(hb.Pubkey)
	m.mutHeartbeatMessages.Lock()
	hbmi, ok := m.heartbeatMessages[pubKeyStr]
	if hbmi == nil || !ok {
		var err error
		peerType := m.computePeerType(hb.Pubkey, hb.ShardID)
		hbmi, err = newHeartbeatMessageInfo(m.maxDurationPeerUnresponsive, peerType, m.genesisTime, m.timer)
		if err != nil {
			log.Debug("error creating hbmi", "error", err.Error())
			m.mutHeartbeatMessages.Unlock()
			return
		}
		m.heartbeatMessages[pubKeyStr] = hbmi
	}
	m.mutHeartbeatMessages.Unlock()

	computedShardID := m.computeShardID(pubKeyStr)
	isInEligibleList := m.computePeerType(hb.Pubkey, computedShardID)

	hbmi.HeartbeatReceived(computedShardID, hb.ShardID, hb.VersionNumber, hb.NodeDisplayName, isInEligibleList)
	hbDTO := m.convertToExportedStruct(hbmi)

	err := m.storer.SavePubkeyData(hb.Pubkey, &hbDTO)
	if err != nil {
		log.Debug("cannot save heartbeat to db", "error", err.Error())
	}
	m.addPeerToFullPeersSlice(hb.Pubkey)
}

func (m *Monitor) addPeerToFullPeersSlice(pubKey []byte) {
	m.mutFullPeersSlice.Lock()
	defer m.mutFullPeersSlice.Unlock()
	if !m.isPeerInFullPeersSlice(pubKey) {
		m.fullPeersSlice = append(m.fullPeersSlice, pubKey)
		err := m.storer.SaveKeys(m.fullPeersSlice)
		if err != nil {
			log.Debug("can't store the keys slice", "error", err.Error())
		}
	}
}

func (m *Monitor) isPeerInFullPeersSlice(pubKey []byte) bool {
	for _, peer := range m.fullPeersSlice {
		if bytes.Equal(peer, pubKey) {
			return true
		}
	}

	return false
}

func (m *Monitor) computeShardID(pubkey string) uint32 {
	// TODO : the shard ID will be recomputed at the end of an epoch / beginning of a new one.
	//  For the moment, just find the shard ID from a copy of the initial pub keys map
	m.mutPubKeysMap.RLock()
	defer m.mutPubKeysMap.RUnlock()
	for shardID, pubKeysSlice := range m.pubKeysMap {
		for _, pKey := range pubKeysSlice {
			if pKey == pubkey {
				return shardID
			}
		}
	}

	// if not found, return the latest known computed shard ID
	return m.heartbeatMessages[pubkey].computedShardID
}

func (m *Monitor) computePeerType(pubkey []byte, shardID uint32) string {
	peerType, err := m.peerTypeProvider.ComputeForPubKey(pubkey, shardID)
	if err != nil {
		log.Warn("monitor: compute peer type", "error", err)
		return string(core.ObserverList)
	}

	return string(peerType)
}

func (m *Monitor) computeAllHeartbeatMessages() {
	m.mutHeartbeatMessages.Lock()
	counterActiveValidators := 0
	counterConnectedNodes := 0
	hbChangedStateToInactiveMap := make(map[string]*heartbeatMessageInfo)
	for key, v := range m.heartbeatMessages {
		previousActive := v.GetIsActive()
		v.ComputeActive(m.timer.Now())
		isActive := v.GetIsActive()

		if isActive {
			counterConnectedNodes++

			if v.GetIsValidator() {
				counterActiveValidators++
			}
		}
		changedStateToInactive := previousActive && !isActive
		if changedStateToInactive {
			hbChangedStateToInactiveMap[key] = v
		}
	}

	m.mutHeartbeatMessages.Unlock()
	go m.SaveMultipleHeartbeatMessageInfos(hbChangedStateToInactiveMap)

	m.appStatusHandler.SetUInt64Value(core.MetricLiveValidatorNodes, uint64(counterActiveValidators))
	m.appStatusHandler.SetUInt64Value(core.MetricConnectedNodes, uint64(counterConnectedNodes))
}

// GetHeartbeats returns the heartbeat status
func (m *Monitor) GetHeartbeats() []PubKeyHeartbeat {
	m.computeAllHeartbeatMessages()

	m.mutHeartbeatMessages.Lock()
	status := make([]PubKeyHeartbeat, len(m.heartbeatMessages))
	idx := 0
	for k, v := range m.heartbeatMessages {
		tmp := PubKeyHeartbeat{
			HexPublicKey:    hex.EncodeToString([]byte(k)),
			TimeStamp:       v.timeStamp,
			MaxInactiveTime: Duration{v.maxInactiveTime},
			IsActive:        v.isActive,
			ReceivedShardID: v.receivedShardID,
			ComputedShardID: v.computedShardID,
			TotalUpTime:     int64(v.totalUpTime.Seconds()),
			TotalDownTime:   int64(v.totalDownTime.Seconds()),
			VersionNumber:   v.versionNumber,
			NodeDisplayName: v.nodeDisplayName,
			PeerType:        v.peerType,
		}
		status[idx] = tmp
		idx++
	}
	m.mutHeartbeatMessages.Unlock()

	sort.Slice(status, func(i, j int) bool {
		return strings.Compare(status[i].HexPublicKey, status[j].HexPublicKey) < 0
	})

	return status
}

// IsInterfaceNil returns true if there is no value under the interface
func (m *Monitor) IsInterfaceNil() bool {
	return m == nil
}

func (m *Monitor) convertToExportedStruct(v *heartbeatMessageInfo) HeartbeatDTO {
	v.updateMutex.Lock()
	defer v.updateMutex.Unlock()
	ret := HeartbeatDTO{
		IsActive:        v.isActive,
		ReceivedShardID: v.receivedShardID,
		ComputedShardID: v.computedShardID,
		VersionNumber:   v.versionNumber,
		NodeDisplayName: v.nodeDisplayName,
		PeerType:        v.peerType,
	}

	ret.TimeStamp = v.timeStamp.UnixNano()
	ret.MaxInactiveTime = v.maxInactiveTime.Nanoseconds()
	ret.TotalUpTime = v.totalUpTime.Nanoseconds()
	ret.TotalDownTime = v.totalDownTime.Nanoseconds()
	ret.LastUptimeDowntime = v.lastUptimeDowntime.UnixNano()
	ret.GenesisTime = v.genesisTime.UnixNano()

	return ret
}

func (m *Monitor) convertFromExportedStruct(hbDTO HeartbeatDTO, maxDuration time.Duration) *heartbeatMessageInfo {
	hbmi := &heartbeatMessageInfo{
		maxDurationPeerUnresponsive: maxDuration,
		isActive:                    hbDTO.IsActive,
		receivedShardID:             hbDTO.ReceivedShardID,
		computedShardID:             hbDTO.ComputedShardID,
		versionNumber:               hbDTO.VersionNumber,
		nodeDisplayName:             hbDTO.NodeDisplayName,
		peerType:                    hbDTO.PeerType,
	}

	hbmi.maxInactiveTime = time.Duration(hbDTO.MaxInactiveTime)
	hbmi.timeStamp = time.Unix(0, hbDTO.TimeStamp)
	hbmi.totalUpTime = time.Duration(hbDTO.TotalUpTime)
	hbmi.totalDownTime = time.Duration(hbDTO.TotalDownTime)
	hbmi.lastUptimeDowntime = time.Unix(0, hbDTO.LastUptimeDowntime)
	hbmi.genesisTime = time.Unix(0, hbDTO.GenesisTime)

	return hbmi
}
