package poolsCleaner

import (
	"sync"
	"time"

	"github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/core/check"
	"github.com/ElrondNetwork/elrond-go/data/block"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/sharding"
	"github.com/ElrondNetwork/elrond-go/storage"
)

var log = logger.GetOrCreate("process/block/poolsCleaner")

const percentAllowed = 0.8

type mbInfo struct {
	round           int64
	senderShardID   uint32
	receiverShardID uint32
	mbType          block.Type
}

// miniBlocksPoolsCleaner represents a pools cleaner that checks and cleans miniblocks which should not be in pool anymore
type miniBlocksPoolsCleaner struct {
	blockTracker     BlockTracker
	miniblocksPool   storage.Cacher
	rounder          process.Rounder
	shardCoordinator sharding.Coordinator

	mutMapMiniBlocksRounds sync.RWMutex
	mapMiniBlocksRounds    map[string]*mbInfo
}

// NewMiniBlocksPoolsCleaner will return a new miniblocks pools cleaner
func NewMiniBlocksPoolsCleaner(
	blockTracker BlockTracker,
	miniblocksPool storage.Cacher,
	rounder process.Rounder,
	shardCoordinator sharding.Coordinator,
) (*miniBlocksPoolsCleaner, error) {

	if check.IfNil(blockTracker) {
		return nil, process.ErrNilBlockTracker
	}
	if check.IfNil(miniblocksPool) {
		return nil, process.ErrNilMiniBlockPool
	}
	if check.IfNil(rounder) {
		return nil, process.ErrNilRounder
	}
	if check.IfNil(shardCoordinator) {
		return nil, process.ErrNilShardCoordinator
	}

	mbpc := miniBlocksPoolsCleaner{
		blockTracker:     blockTracker,
		miniblocksPool:   miniblocksPool,
		rounder:          rounder,
		shardCoordinator: shardCoordinator,
	}

	mbpc.mapMiniBlocksRounds = make(map[string]*mbInfo)
	mbpc.miniblocksPool.RegisterHandler(mbpc.receivedMiniBlock)

	go mbpc.cleanMiniblocksPools()

	return &mbpc, nil
}

func (mbpc *miniBlocksPoolsCleaner) cleanMiniblocksPools() {
	for {
		time.Sleep(sleepTime)
		numMiniblocksInMap := mbpc.cleanMiniblocksPoolsIfNeeded()
		log.Debug("miniBlocksPoolsCleaner.cleanMiniblocksPools", "num miniblocks in map", numMiniblocksInMap)

	}
}

func (mbpc *miniBlocksPoolsCleaner) receivedMiniBlock(key []byte, value interface{}) {
	if key == nil {
		return
	}

	miniBlock, ok := value.(*block.MiniBlock)
	if !ok {
		log.Warn("miniBlocksPoolsCleaner.receivedMiniBlock", "error", process.ErrWrongTypeAssertion)
		return
	}

	log.Trace("miniBlocksPoolsCleaner.receivedMiniBlock", "hash", key)

	mbpc.mutMapMiniBlocksRounds.Lock()
	defer mbpc.mutMapMiniBlocksRounds.Unlock()

	if _, ok := mbpc.mapMiniBlocksRounds[string(key)]; !ok {
		receivedMbInfo := &mbInfo{
			round:           mbpc.rounder.Index(),
			senderShardID:   miniBlock.SenderShardID,
			receiverShardID: miniBlock.ReceiverShardID,
			mbType:          miniBlock.Type,
		}

		mbpc.mapMiniBlocksRounds[string(key)] = receivedMbInfo

		log.Trace("miniblock has been added",
			"hash", key,
			"round", receivedMbInfo.round,
			"sender shard", receivedMbInfo.senderShardID,
			"receiver shard", receivedMbInfo.receiverShardID,
			"type", receivedMbInfo.mbType)
	}
}

func (mbpc *miniBlocksPoolsCleaner) cleanMiniblocksPoolsIfNeeded() int {
	mbpc.mutMapMiniBlocksRounds.Lock()
	defer mbpc.mutMapMiniBlocksRounds.Unlock()

	selfShardID := mbpc.shardCoordinator.SelfId()
	numPendingMiniBlocks := mbpc.blockTracker.GetNumPendingMiniBlocks(selfShardID)
	percentUsed := float64(mbpc.miniblocksPool.Len()) / float64(mbpc.miniblocksPool.MaxSize())
	numMbsCleaned := 0

	for hash, mbInfo := range mbpc.mapMiniBlocksRounds {
		if mbInfo.senderShardID != selfShardID {
			if numPendingMiniBlocks > 0 && percentUsed < percentAllowed {
				log.Trace("cleaning cross miniblock not yet allowed",
					"hash", []byte(hash),
					"round", mbInfo.round,
					"num pending miniblocks", numPendingMiniBlocks,
					"miniblocks pool percent used", percentUsed,
					"type", mbInfo.mbType,
					"sender", mbInfo.senderShardID,
					"receiver", mbInfo.receiverShardID)

				continue
			}
		}

		roundDif := mbpc.rounder.Index() - mbInfo.round
		if roundDif <= process.MaxRoundsToKeepUnprocessedMiniBlocks {
			log.Trace("cleaning miniblock not yet allowed",
				"hash", []byte(hash),
				"round", mbInfo.round,
				"round dif", roundDif,
				"type", mbInfo.mbType,
				"sender", mbInfo.senderShardID,
				"receiver", mbInfo.receiverShardID)

			continue
		}

		mbpc.miniblocksPool.Remove([]byte(hash))
		delete(mbpc.mapMiniBlocksRounds, hash)
		numMbsCleaned++

		log.Trace("miniblock has been cleaned",
			"hash", []byte(hash),
			"round", mbInfo.round,
			"type", mbInfo.mbType,
			"sender", mbInfo.senderShardID,
			"receiver", mbInfo.receiverShardID)
	}

	if numMbsCleaned > 0 {
		log.Debug("miniBlocksPoolsCleaner.cleanMiniblocksPoolsIfNeeded",
			"num mbs cleaned", numMbsCleaned)
	}

	return len(mbpc.mapMiniBlocksRounds)
}