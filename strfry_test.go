package strfry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNegentropyUnsupportedLog(t *testing.T) {
	unsupportedLogs := []string{
		`2024-09-27 10:53:46.663 (   1.121s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","ERROR: bad msg: invalid message: {'message_type': ['Invalid enum value NEG-OPEN']}"]`,
		`2024-09-27 10:55:45.318 (   0.733s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","bad message type"]`,
		`2024-09-27 10:53:10.321 (   0.796s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","ERROR: bad msg: negentropy disabled"]`,
		`2024-09-27 10:45:58.450 (   1.072s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","ERROR: negentropy error: negentropy query missing elements"]`,
		`2024-09-27 15:07:30.208 (   1.021s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","invalid: \"value\" does not match any of the allowed types"]`,
		`2024-09-27 15:11:43.411 (   2.961s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","Command unrecognized"]`,
		`2024-09-27 15:15:17.578 (   0.839s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","error: bad message"]`,
		`2024-09-27 15:16:52.966 (   1.088s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","could not parse command"]`,
		`2024-09-27 15:16:52.966 (   1.088s) [main thread     ]WARN| Unexpected message from relay: ["NOTICE","unknown message type NEG-OPEN"]`,
	}

	otherLogs := []string{
		"something else",
	}

	for _, log := range unsupportedLogs {
		unsupported := NegentropyUnsupportedLog([]byte(log))
		assert.Equal(t, true, unsupported)
	}

	for _, log := range otherLogs {
		unsupported := NegentropyUnsupportedLog([]byte(log))
		assert.Equal(t, false, unsupported)
	}
}
