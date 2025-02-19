// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package auth

import (
	"fmt"
	"runtime/pprof"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"

	"github.com/cilium/cilium/pkg/auth/spire"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/k8s"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/resource"
	"github.com/cilium/cilium/pkg/maps/authmap"
	"github.com/cilium/cilium/pkg/signal"
	"github.com/cilium/cilium/pkg/stream"
)

// Cell invokes authManager which is responsible for request authentication.
// It does this by registering to "auth required" signals from the signal package
// and reacting upon received signal events.
// Actual authentication gets performed by an auth handler which is
// responsible for the configured auth type on the corresponding policy.
var Cell = cell.Module(
	"auth",
	"Authenticates requests as demanded by policy",

	spire.Cell,

	// The manager is the main entry point which gets registered to signal map and receives auth requests.
	cell.Invoke(newManager),
	cell.ProvidePrivate(
		// Null auth handler provides support for auth type "null" - which always succeeds.
		newMutualAuthHandler,
		// Always fail auth handler provides support for auth type "always-fail" - which always fails.
		newAlwaysFailAuthHandler,
	),
	// Providing k8s resource Node & Identity privately to avoid further usage of them in other agent components
	cell.ProvidePrivate(
		// TODO: use node manager to get events of all nodes, including the ones of other clusters (ClusterMesh)
		// https://github.com/cilium/cilium/issues/25899
		k8s.CiliumNodeResource,
		// TODO: add support for KVStore. K8s identity events are only provided for CRD based identity backend.
		// https://github.com/cilium/cilium/issues/25898
		k8s.CiliumIdentityResource,
	),
	cell.Config(config{
		MeshAuthQueueSize:         1024,
		MeshAuthExpiredGCInterval: 15 * time.Minute,
	}),
	cell.Config(MutualAuthConfig{}),
)

type config struct {
	MeshAuthQueueSize         int
	MeshAuthExpiredGCInterval time.Duration
}

func (r config) Flags(flags *pflag.FlagSet) {
	flags.Int("mesh-auth-queue-size", r.MeshAuthQueueSize, "Queue size for the auth manager")
	flags.Duration("mesh-auth-expired-gc-interval", r.MeshAuthExpiredGCInterval, "Interval in which expired auth entries are attempted to be garbage collected")
}

type authManagerParams struct {
	cell.In

	Logger           logrus.FieldLogger
	Lifecycle        hive.Lifecycle
	JobRegistry      job.Registry
	Config           config
	IPCache          *ipcache.IPCache
	AuthHandlers     []authHandler `group:"authHandlers"`
	AuthMap          authmap.Map
	SignalManager    signal.SignalManager
	CiliumIdentities resource.Resource[*ciliumv2.CiliumIdentity]
	CiliumNodes      resource.Resource[*ciliumv2.CiliumNode]
}

func newManager(params authManagerParams) error {
	mapWriter := newAuthMapWriter(params.Logger, params.AuthMap)
	mapCache := newAuthMapCache(params.Logger, mapWriter)

	params.Lifecycle.Append(hive.Hook{
		OnStart: func(hookContext hive.HookContext) error {
			if err := mapCache.restoreCache(); err != nil {
				return fmt.Errorf("failed to restore auth map cache: %w", err)
			}

			return nil
		},
	})

	jobGroup := params.JobRegistry.NewGroup(
		job.WithLogger(params.Logger),
		job.WithPprofLabels(pprof.Labels("cell", "auth")),
	)

	mgr, err := newAuthManager(params.Logger, params.AuthHandlers, mapCache, params.IPCache)
	if err != nil {
		return fmt.Errorf("failed to create auth manager: %w", err)
	}

	if err := registerSignalAuthenticationJob(jobGroup, mgr, params.SignalManager, params.Config); err != nil {
		return fmt.Errorf("failed to register signal authentication job: %w", err)
	}

	registerReAuthenticationJob(jobGroup, mgr, params.AuthHandlers)

	mapGC := newAuthMapGC(params.Logger, mapCache, params.IPCache)

	registerGCJobs(jobGroup, mapGC, params)

	params.Lifecycle.Append(jobGroup)

	return nil
}

func registerReAuthenticationJob(jobGroup job.Group, mgr *authManager, authHandlers []authHandler) {
	for _, ah := range authHandlers {
		if ah != nil && ah.subscribeToRotatedIdentities() != nil {
			jobGroup.Add(job.Observer("auth re-authentication", mgr.handleCertificateRotationEvent, stream.FromChannel(ah.subscribeToRotatedIdentities())))
		}
	}
}

func registerSignalAuthenticationJob(jobGroup job.Group, mgr *authManager, sm signal.SignalManager, config config) error {
	var signalChannel = make(chan signalAuthKey, config.MeshAuthQueueSize)

	// RegisterHandler registers signalChannel with SignalManager, but flow of events
	// starts later during the OnStart hook of the SignalManager
	if err := sm.RegisterHandler(signal.ChannelHandler(signalChannel), signal.SignalAuthRequired); err != nil {
		return fmt.Errorf("failed to set up signal channel for datapath authentication required events: %w", err)
	}

	jobGroup.Add(job.Observer("auth request processing", mgr.handleAuthRequest, stream.FromChannel(signalChannel)))

	return nil
}

func registerGCJobs(jobGroup job.Group, mapGC *authMapGarbageCollector, params authManagerParams) {
	// Add identities based auth gc if k8s client is enabled
	if params.CiliumIdentities != nil {
		jobGroup.Add(job.Observer[resource.Event[*ciliumv2.CiliumIdentity]]("auth identities gc", mapGC.handleCiliumIdentityEvent, params.CiliumIdentities))
	}

	// Add node based auth gc if k8s client is enabled
	if params.CiliumNodes != nil {
		jobGroup.Add(job.Observer[resource.Event[*ciliumv2.CiliumNode]]("auth nodes gc", mapGC.handleCiliumNodeEvent, params.CiliumNodes))
	}

	jobGroup.Add(job.Timer("auth expiration gc", mapGC.CleanupExpiredEntries, params.Config.MeshAuthExpiredGCInterval))
}

type authHandlerResult struct {
	cell.Out

	AuthHandler authHandler `group:"authHandlers"`
}
