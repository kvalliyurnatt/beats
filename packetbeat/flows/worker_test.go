// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package flows

import (
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/elastic/go-lookslike/isdef"
	"github.com/stretchr/testify/assert"

	"github.com/elastic/go-lookslike"

	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/packetbeat/procs"
	"github.com/elastic/elastic-agent-libs/logp"
)

// Use `go test -data` to update sample event files.
var dataFlag = flag.Bool("data", false, "Write updated data.json files")

func TestCreateEvent(t *testing.T) {
	logp.TestingSetup()

	// Build biflow event.
	start := time.Unix(1542292881, 0)
	end := start.Add(3 * time.Second)
	vlan := uint16(171)
	mac1 := []byte{1, 2, 3, 4, 5, 6}
	mac2 := []byte{6, 5, 4, 3, 2, 1}
	ip1 := []byte{203, 0, 113, 3}
	ip2 := []byte{198, 51, 100, 2}
	port1 := uint16(38901)
	port2 := uint16(80)

	id := newFlowID()
	id.AddEth(mac1, mac2)
	id.AddVLan(vlan)
	id.AddIPv4(ip1, ip2)
	id.AddTCP(port1, port2)

	bif := &biFlow{
		id:       id.rawFlowID,
		killed:   1,
		createTS: start,
		ts:       end,
		dir:      flowDirForward,
	}
	bif.stats[0] = &flowStats{uintFlags: []uint8{1, 1}, uints: []uint64{10, 1}}
	bif.stats[1] = &flowStats{uintFlags: []uint8{1, 1}, uints: []uint64{460, 2}}
	event := createEvent(&procs.ProcessesWatcher{}, time.Now(), bif, true, nil, []string{"bytes", "packets"}, nil, NoKill)

	// Validate the contents of the event.
	validate := lookslike.MustCompile(map[string]interface{}{
		"source": map[string]interface{}{
			"mac":     "01-02-03-04-05-06",
			"ip":      "203.0.113.3",
			"port":    port1,
			"bytes":   uint64(10),
			"packets": uint64(1),
		},
		"destination": map[string]interface{}{
			"mac":     "06-05-04-03-02-01",
			"ip":      "198.51.100.2",
			"port":    port2,
			"bytes":   uint64(460),
			"packets": uint64(2),
		},
		"flow": map[string]interface{}{
			"id":    isdef.KeyPresent,
			"final": true,
			"vlan":  isdef.KeyPresent,
		},
		"network": map[string]interface{}{
			"bytes":     uint64(470),
			"packets":   uint64(3),
			"type":      "ipv4",
			"transport": "tcp",
		},
		"event": map[string]interface{}{
			"start":    isdef.KeyPresent,
			"end":      isdef.KeyPresent,
			"duration": isdef.KeyPresent,
			"dataset":  "flow",
			"kind":     "event",
			"category": []string{"network"},
			"action":   "network_flow",
		},
		"type": "flow",
	})

	result := validate(event.Fields)
	if errs := result.Errors(); len(errs) > 0 {
		for _, err := range errs {
			t.Error(err)
		}
		t.FailNow()
	}

	// Write the event to disk if -data is used.
	if *dataFlag {
		event.Fields.Put("@timestamp", common.Time(end)) //nolint:errcheck // Never fails.
		output, err := json.MarshalIndent(&event.Fields, "", "  ")
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile("../_meta/sample_outputs/flow.json", output, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func Test_shouldKillFlow(t *testing.T) {
	logp.TestingSetup()

	// Build biflow event.
	vlan := uint16(171)
	mac1 := []byte{1, 2, 3, 4, 5, 6}
	mac2 := []byte{6, 5, 4, 3, 2, 1}
	ip1 := []byte{203, 0, 113, 3}
	ip2 := []byte{198, 51, 100, 2}
	port1 := uint16(38901)
	port2 := uint16(80)

	id := newFlowID()
	id.AddEth(mac1, mac2)
	id.AddVLan(vlan)
	id.AddIPv4(ip1, ip2)
	id.AddTCP(port1, port2)
	processor := &flowsProcessor{}

	type args struct {
		flow                 *biFlow
		flowProcessorTimeout time.Duration
		currentTime          time.Time
		activeTimeoutEnabled bool
	}
	tests := []struct {
		name       string
		args       args
		want       flowKillReason
		flowKilled bool
	}{
		{
			name: "Test active timeout",
			args: args{
				flow: &biFlow{
					id:       id.rawFlowID,
					killed:   1,
					dir:      flowDirForward,
					createTS: time.Now(),
					ts:       time.Now().Add(40 * time.Second),
				},
				flowProcessorTimeout: 30 * time.Second,
				currentTime:          time.Now().Add(50 * time.Second),
				activeTimeoutEnabled: true,
			},
			want:       ActiveTimeout,
			flowKilled: true,
		},
		{
			name: "Test idle timeout",
			args: args{
				flow: &biFlow{
					id:       id.rawFlowID,
					killed:   1,
					dir:      flowDirForward,
					createTS: time.Now(),
					ts:       time.Now(),
				},
				flowProcessorTimeout: 30 * time.Second,
				currentTime:          time.Now().Add(30 * time.Second),
				activeTimeoutEnabled: false,
			},
			want:       IdleTimeout,
			flowKilled: true,
		},
		{
			name: "Test flow should not be killed",
			args: args{
				flow: &biFlow{
					id:       id.rawFlowID,
					killed:   1,
					dir:      flowDirForward,
					createTS: time.Now(),
					ts:       time.Now().Add(40 * time.Second),
				},
				flowProcessorTimeout: 30 * time.Second,
				currentTime:          time.Now().Add(50 * time.Second),
				activeTimeoutEnabled: false,
			},
			want:       NoKill,
			flowKilled: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor.timeout = tt.args.flowProcessorTimeout
			killReason, killFlow := shouldKillFlow(tt.args.flow, processor, tt.args.currentTime, tt.args.activeTimeoutEnabled)

			assert.Equal(t, tt.flowKilled, killFlow)
			assert.Equal(t, tt.want, killReason)
		})
	}
}
