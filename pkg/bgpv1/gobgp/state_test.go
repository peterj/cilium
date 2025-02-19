// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package gobgp

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"k8s.io/utils/pointer"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/bgpv1/types"
	v2alpha1api "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2alpha1"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	log = logging.DefaultLogger.WithField(logfields.LogSubsys, "bgp-test")

	neighbor64125 = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64125,
		PeerAddress:      "192.168.0.1/32",
		PeerPort:         pointer.Int(types.DefaultPeerPort),
		ConnectRetryTime: metav1.Duration{Duration: 99 * time.Second},
		HoldTime:         metav1.Duration{Duration: 9 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 3 * time.Second},
	}

	// changed ConnectRetryTime
	neighbor64125Update = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64125,
		PeerAddress:      "192.168.0.1/32",
		PeerPort:         pointer.Int(types.DefaultPeerPort),
		ConnectRetryTime: metav1.Duration{Duration: 101 * time.Second},
		HoldTime:         metav1.Duration{Duration: 9 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 3 * time.Second},
	}

	// enabled graceful restart
	neighbor64125UpdateGR = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64125,
		PeerAddress:      "192.168.0.1/32",
		PeerPort:         pointer.Int(types.DefaultPeerPort),
		ConnectRetryTime: metav1.Duration{Duration: 101 * time.Second},
		HoldTime:         metav1.Duration{Duration: 30 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 10 * time.Second},
		GracefulRestart: v2alpha1api.CiliumBGPNeighborGracefulRestart{
			Enabled:     true,
			RestartTime: metav1.Duration{Duration: 120 * time.Second},
		},
	}

	// enabled graceful restart - updated restart time
	neighbor64125UpdateGRTimer = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64125,
		PeerAddress:      "192.168.0.1/32",
		PeerPort:         pointer.Int(types.DefaultPeerPort),
		ConnectRetryTime: metav1.Duration{Duration: 101 * time.Second},
		HoldTime:         metav1.Duration{Duration: 30 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 10 * time.Second},
		GracefulRestart: v2alpha1api.CiliumBGPNeighborGracefulRestart{
			Enabled:     true,
			RestartTime: metav1.Duration{Duration: 20 * time.Second},
		},
	}

	neighbor64126 = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64126,
		PeerAddress:      "192.168.66.1/32",
		PeerPort:         pointer.Int(types.DefaultPeerPort),
		ConnectRetryTime: metav1.Duration{Duration: 99 * time.Second},
		HoldTime:         metav1.Duration{Duration: 9 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 3 * time.Second},
	}

	// changed HoldTime & KeepAliveTime
	neighbor64126Update = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64126,
		PeerAddress:      "192.168.66.1/32",
		PeerPort:         pointer.Int(types.DefaultPeerPort),
		ConnectRetryTime: metav1.Duration{Duration: 99 * time.Second},
		HoldTime:         metav1.Duration{Duration: 12 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 4 * time.Second},
	}

	neighbor64127 = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64127,
		PeerAddress:      "192.168.88.1/32",
		ConnectRetryTime: metav1.Duration{Duration: 99 * time.Second},
		HoldTime:         metav1.Duration{Duration: 9 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 3 * time.Second},
	}

	// changed EBGPMultihopTTL
	neighbor64127Update = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64127,
		PeerAddress:      "192.168.88.1/32",
		ConnectRetryTime: metav1.Duration{Duration: 99 * time.Second},
		HoldTime:         metav1.Duration{Duration: 9 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 3 * time.Second},
		EBGPMultihopTTL:  10,
	}

	neighbor64128 = &v2alpha1api.CiliumBGPNeighbor{
		PeerASN:          64128,
		PeerAddress:      "192.168.77.1/32",
		PeerPort:         pointer.Int(types.DefaultPeerPort),
		ConnectRetryTime: metav1.Duration{Duration: 99 * time.Second},
		HoldTime:         metav1.Duration{Duration: 9 * time.Second},
		KeepAliveTime:    metav1.Duration{Duration: 3 * time.Second},
	}
)

// TestGetPeerState confirms the parsing of go bgp ListPeers to cilium modes work as intended
func TestGetPeerState(t *testing.T) {
	var table = []struct {
		// name of the test
		name string
		// neighbors to configure
		neighbors []*v2alpha1api.CiliumBGPNeighbor
		// neighbors to update
		neighborsAfterUpdate []*v2alpha1api.CiliumBGPNeighbor
		// localASN is local autonomous number
		localASN uint32
		// expected error message on AddNeighbor() or empty string for no error
		errStr string
		// expected error message  on UpdateNeighbor() or empty string for no error
		updateErrStr string
	}{
		{
			name:      "test add neighbor",
			neighbors: []*v2alpha1api.CiliumBGPNeighbor{neighbor64125},
			localASN:  64124,
			errStr:    "",
		},
		{
			name: "test add neighbor with port",
			neighbors: []*v2alpha1api.CiliumBGPNeighbor{
				{
					PeerASN:          64125,
					PeerAddress:      "192.168.0.1/32",
					PeerPort:         pointer.Int(175),
					ConnectRetryTime: metav1.Duration{Duration: 99 * time.Second},
					HoldTime:         metav1.Duration{Duration: 9 * time.Second},
					KeepAliveTime:    metav1.Duration{Duration: 3 * time.Second},
				},
			},
			localASN: 64124,
			errStr:   "",
		},
		{
			name: "test add + update neighbors",
			neighbors: []*v2alpha1api.CiliumBGPNeighbor{
				neighbor64125,
				neighbor64126,
				neighbor64127,
				neighbor64128,
			},
			neighborsAfterUpdate: []*v2alpha1api.CiliumBGPNeighbor{
				// changed ConnectRetryTime
				neighbor64125Update,
				// changed HoldTime & KeepAliveTime
				neighbor64126Update,
				// changed EBGPMultihopTTL
				neighbor64127Update,
				// no change
				neighbor64128,
			},
			localASN: 64124,
			errStr:   "",
		},
		{
			name: "test graceful restart - update enable",
			neighbors: []*v2alpha1api.CiliumBGPNeighbor{
				neighbor64125,
			},
			neighborsAfterUpdate: []*v2alpha1api.CiliumBGPNeighbor{
				// enabled GR
				neighbor64125UpdateGR,
			},
			localASN: 64124,
			errStr:   "",
		},
		{
			name: "test graceful restart - update restart time",
			neighbors: []*v2alpha1api.CiliumBGPNeighbor{
				neighbor64125UpdateGR,
			},
			neighborsAfterUpdate: []*v2alpha1api.CiliumBGPNeighbor{
				// changed gr restart time
				neighbor64125UpdateGRTimer,
			},
			localASN: 64124,
			errStr:   "",
		},
		{
			name: "test add invalid neighbor",
			neighbors: []*v2alpha1api.CiliumBGPNeighbor{
				// invalid PeerAddress
				{
					PeerASN:          64125,
					PeerAddress:      "192.168.0.XYZ",
					ConnectRetryTime: metav1.Duration{Duration: 101 * time.Second},
					HoldTime:         metav1.Duration{Duration: 30 * time.Second},
					KeepAliveTime:    metav1.Duration{Duration: 10 * time.Second},
				},
			},
			localASN: 64124,
			errStr:   "failed to parse PeerAddress: netip.ParsePrefix(\"192.168.0.XYZ\"): no '/'",
		},
		{
			name: "test invalid neighbor update",
			neighbors: []*v2alpha1api.CiliumBGPNeighbor{
				{
					PeerASN:          64125,
					PeerAddress:      "192.168.0.1/32",
					ConnectRetryTime: metav1.Duration{Duration: 101 * time.Second},
					HoldTime:         metav1.Duration{Duration: 30 * time.Second},
					KeepAliveTime:    metav1.Duration{Duration: 10 * time.Second},
				},
			},
			neighborsAfterUpdate: []*v2alpha1api.CiliumBGPNeighbor{
				// different ASN
				{
					PeerASN:          64999,
					PeerAddress:      "192.168.0.1/32",
					ConnectRetryTime: metav1.Duration{Duration: 101 * time.Second},
					HoldTime:         metav1.Duration{Duration: 30 * time.Second},
					KeepAliveTime:    metav1.Duration{Duration: 10 * time.Second},
				},
			},
			localASN:     64124,
			errStr:       "",
			updateErrStr: "failed retrieving peer: could not find existing peer with ASN: 64999 and IP: 192.168.0.1",
		},
	}
	for _, tt := range table {
		srvParams := types.ServerParameters{
			Global: types.BGPGlobal{
				ASN:        tt.localASN,
				RouterID:   "127.0.0.1",
				ListenPort: -1,
			},
		}
		t.Run(tt.name, func(t *testing.T) {
			testSC, err := NewGoBGPServerWithConfig(context.Background(), log, srvParams)
			require.NoError(t, err)

			t.Cleanup(func() {
				testSC.Stop()
			})

			// add neighbours
			for _, n := range tt.neighbors {
				err = testSC.AddNeighbor(context.Background(), types.NeighborRequest{
					Neighbor: n,
				})
				if tt.errStr != "" {
					require.EqualError(t, err, tt.errStr)
					return // no more checks
				} else {
					require.NoError(t, err)
				}
			}

			res, err := testSC.GetPeerState(context.Background())
			require.NoError(t, err)

			// validate neighbors count
			require.Len(t, res.Peers, len(tt.neighbors))

			// validate peers
			validatePeers(t, tt.localASN, tt.neighbors, res.Peers)

			// update neighbours
			for _, n := range tt.neighborsAfterUpdate {
				err = testSC.UpdateNeighbor(context.Background(), types.NeighborRequest{
					Neighbor: n,
				})
				if tt.updateErrStr != "" {
					require.EqualError(t, err, tt.updateErrStr)
					return // no more checks
				} else {
					require.NoError(t, err)
				}
			}

			res, err = testSC.GetPeerState(context.Background())
			require.NoError(t, err)

			// validate peers
			validatePeers(t, tt.localASN, tt.neighborsAfterUpdate, res.Peers)
		})
	}
}

// validatePeers validates that peers returned from GoBGP GetPeerState match expected list of CiliumBGPNeighbors
func validatePeers(t *testing.T, localASN uint32, neighbors []*v2alpha1api.CiliumBGPNeighbor, peers []*models.BgpPeer) {
	for _, n := range neighbors {
		p := findMatchingPeer(t, peers, n)
		require.NotNilf(t, p, "no matching peer for PeerASN %d and PeerAddress %s", n.PeerASN, n.PeerAddress)

		// validate basic data is returned correctly
		require.Equal(t, int64(localASN), p.LocalAsn)
		require.EqualValues(t, n.ConnectRetryTime.Seconds(), p.ConnectRetryTimeSeconds)
		require.EqualValues(t, n.HoldTime.Seconds(), p.ConfiguredHoldTimeSeconds)
		require.EqualValues(t, n.KeepAliveTime.Seconds(), p.ConfiguredKeepAliveTimeSeconds)

		require.EqualValues(t, n.GracefulRestart.Enabled, p.GracefulRestart.Enabled)
		require.EqualValues(t, n.GracefulRestart.RestartTime.Seconds(), p.GracefulRestart.RestartTimeSeconds)

		if n.EBGPMultihopTTL > 0 {
			require.EqualValues(t, n.EBGPMultihopTTL, p.EbgpMultihopTTL)
		}

		// since there is no real neighbor, bgp session state will be either idle or active.
		require.Contains(t, []string{"idle", "active"}, p.SessionState)
	}
}

// findMatchingPeer finds models.BgpPeer matching to the provided v2alpha1api.CiliumBGPNeighbor based on the peer ASN and IP
func findMatchingPeer(t *testing.T, peers []*models.BgpPeer, n *v2alpha1api.CiliumBGPNeighbor) *models.BgpPeer {
	for _, p := range peers {
		nPrefix, err := netip.ParsePrefix(n.PeerAddress)
		require.NoError(t, err)
		pIP, err := netip.ParseAddr(p.PeerAddress)
		require.NoError(t, err)

		if p.PeerAsn == int64(n.PeerASN) && pIP.Compare(nPrefix.Addr()) == 0 {
			return p
		}
	}
	return nil
}
