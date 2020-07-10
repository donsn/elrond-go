package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	logger "github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/api/shared"
	"github.com/gin-gonic/gin"
)

var log = logger.GetOrCreate("api/middleware")

// globalThrottler is a middleware global limiter used to limit total number of simultaneous requests
type globalThrottler struct {
	queue chan struct{}

	mutDebug  sync.RWMutex
	debugInfo map[string]int
}

// NewGlobalThrottler creates a new instance of a globalThrottler
func NewGlobalThrottler(maxConnections uint32) (*globalThrottler, error) {
	if maxConnections == 0 {
		return nil, ErrInvalidMaxNumRequests
	}

	return &globalThrottler{
		queue:     make(chan struct{}, maxConnections),
		debugInfo: make(map[string]int),
	}, nil
}

// MiddlewareHandlerFunc returns the handler func used by the gin server when processing requests
func (gt *globalThrottler) MiddlewareHandlerFunc() gin.HandlerFunc {
	return func(c *gin.Context) {
		select {
		case gt.queue <- struct{}{}:
			gt.mutDebug.Lock()
			gt.debugInfo[c.Request.URL.Path]++
			gt.mutDebug.Unlock()
		default:
			c.AbortWithStatusJSON(
				http.StatusTooManyRequests,
				shared.GenericAPIResponse{
					Data:  nil,
					Error: "too many requests to observer",
					Code:  shared.ReturnCodeSystemBusy,
				},
			)

			output := make([]string, 0)
			gt.mutDebug.Lock()
			for route, num := range gt.debugInfo {
				output = append(output, fmt.Sprintf("%s: %d", route, num))
			}
			gt.mutDebug.Unlock()

			log.Warn("system busy\n" + strings.Join(output, "\n"))

			return
		}

		c.Next()

		gt.mutDebug.Lock()
		gt.debugInfo[c.Request.URL.Path]--
		if gt.debugInfo[c.Request.URL.Path] == 0 {
			delete(gt.debugInfo, c.Request.URL.Path)
		}
		gt.mutDebug.Unlock()

		<-gt.queue
	}
}

// IsInterfaceNil returns true if there is no value under the interface
func (gt *globalThrottler) IsInterfaceNil() bool {
	return gt == nil
}
