//go:build moose

// Package moose provides convenient wrappers for event sending.
package moose

// #cgo amd64 LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-nordvpnapp/current/amd64
// #cgo amd64 LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-worker/current/amd64
// #cgo 386 LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-nordvpnapp/current/i386
// #cgo 386 LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-worker/current/i386
// #cgo arm LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-nordvpnapp/current/armel
// #cgo arm LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-worker/current/armel
// #cgo arm LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-nordvpnapp/current/armhf
// #cgo arm LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-worker/current/armhf
// #cgo arm64 LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-nordvpnapp/current/aarch64
// #cgo arm64 LDFLAGS: -L${SRCDIR}/../../bin/deps/lib/libmoose-worker/current/aarch64
// #cgo LDFLAGS: -ldl -lm -lmoosenordvpnapp -lmooseworker -lsqlite3
import "C"

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NordSecurity/nordvpn-linux/config"
	"github.com/NordSecurity/nordvpn-linux/core"
	"github.com/NordSecurity/nordvpn-linux/distro"
	"github.com/NordSecurity/nordvpn-linux/events"
	"github.com/NordSecurity/nordvpn-linux/internal"
	"github.com/NordSecurity/nordvpn-linux/snapconf"

	moose "moose/events"
	worker "moose/worker"
)

// Subscriber listen events, send to moose engine
type Subscriber struct {
	EventsDbPath            string
	Config                  config.Manager
	Version                 string
	Environment             string
	Domain                  string
	Subdomain               string
	DeviceID                string
	SubscriptionAPI         core.SubscriptionAPI
	currentDomain           string
	connectionStartTime     time.Time
	connectionToMeshnetPeer bool
	enabled                 bool
	initialHeartbeatSent    bool
	mux                     sync.RWMutex
}

// Enable moose analytics engine
func (s *Subscriber) Enable() error {
	s.mux.Lock()
	defer s.mux.Unlock()
	if s.enabled {
		return nil
	}
	s.enabled = true
	return s.response(moose.MooseNordvpnappSetOptIn(true))
}

// Disable moose analytics engine
func (s *Subscriber) Disable() error {
	s.mux.Lock()
	defer s.mux.Unlock()
	if !s.enabled {
		return nil
	}
	s.enabled = false
	return s.response(moose.MooseNordvpnappSetOptIn(false))
}

func (s *Subscriber) isEnabled() bool {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.enabled
}

// Init initializes moose libs. It has to be done before usage regardless of the enabled state.
// Disabled case should be handled by `set_opt_out` value.
func (s *Subscriber) Init() error {
	s.mux.Lock()
	defer s.mux.Unlock()
	var cfg config.Config
	if err := s.Config.Load(&cfg); err != nil {
		return err
	}

	deviceType := "server"
	if _, err := exec.LookPath("xrandr"); err == nil {
		deviceType = "desktop"
	}

	err := s.updateEventDomain()
	if err != nil {
		return fmt.Errorf("initializing event domain: %w", err)
	}

	timeBetweenEvents := 100 * time.Millisecond
	timeBetweenBatchesOfEvents := time.Second
	if internal.IsProdEnv(s.Environment) {
		timeBetweenEvents = 2 * time.Second
		timeBetweenBatchesOfEvents = 2 * time.Hour
	}
	sendEvents := true
	var batchSize uint32 = 20
	compressRequest := true

	if err := s.response(uint32(worker.Start(
		s.EventsDbPath,
		s.currentDomain,
		uint64(timeBetweenEvents.Milliseconds()),
		uint64(timeBetweenBatchesOfEvents.Milliseconds()),
		sendEvents,
		batchSize,
		compressRequest,
	))); err != nil {
		return fmt.Errorf("starting worker: %w", err)
	}

	s.enabled = cfg.Analytics.Get()
	if err := s.response(moose.MooseNordvpnappInit(
		s.EventsDbPath,
		internal.IsProdEnv(s.Environment),
		s,
		s,
		s.enabled,
	)); err != nil {
		if !strings.Contains(err.Error(), "moose: already initiated") {
			return fmt.Errorf("starting tracker: %w", err)
		}
	}

	applicationName := "linux-app"
	if snapconf.IsUnderSnap() {
		applicationName = "linux-app-snap"
	}

	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappName(applicationName)); err != nil {
		return fmt.Errorf("setting application name: %w", err)
	}

	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappVersion(s.Version)); err != nil {
		return fmt.Errorf("setting application version: %w", err)
	}

	if err := s.response(moose.NordvpnappSetContextDeviceTimeZone(internal.Timezone())); err != nil {
		return fmt.Errorf("setting moose time zone: %w", err)
	}

	distroVersion, err := distro.ReleasePrettyName()
	if err != nil {
		return fmt.Errorf("determining device os: %w", err)
	}
	if err := s.response(moose.NordvpnappSetContextDeviceOs(distroVersion)); err != nil {
		return fmt.Errorf("setting moose device os: %w", err)
	}
	if err := s.response(moose.NordvpnappSetContextDeviceFp(s.DeviceID)); err != nil {
		return fmt.Errorf("setting moose device: %w", err)
	}
	var deviceT moose.NordvpnappDeviceType
	switch deviceType {
	case "desktop":
		deviceT = moose.NordvpnappDeviceTypeDesktop
	case "server":
		deviceT = moose.NordvpnappDeviceTypeServer
	default:
		deviceT = moose.NordvpnappDeviceTypeUndefined
	}
	if err := s.response(moose.NordvpnappSetContextDeviceType(deviceT)); err != nil {
		return fmt.Errorf("setting moose device type: %w", err)
	}

	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateIsOnVpnValue(false)); err != nil {
		return fmt.Errorf("setting moose is on vpn: %w", err)
	}

	sub := &Subscriber{}
	if err := sub.NotifyFirewall(true); err != nil {
		return fmt.Errorf("setting moose firewall: %w", err)
	}

	if err := sub.NotifyProtocol(cfg.AutoConnectData.Protocol); err != nil {
		return fmt.Errorf("setting moose protocol: %w", err)
	}

	if err := sub.NotifyTechnology(cfg.Technology); err != nil {
		return fmt.Errorf("setting moose technology: %w", err)
	}
	return nil
}

func (s *Subscriber) Stop() error {
	if err := s.response(moose.MooseNordvpnappFlushChanges()); err != nil {
		return fmt.Errorf("stopping moose worker: %w", err)
	}
	if err := s.response(worker.Stop()); err != nil {
	}
	return s.response(moose.MooseNordvpnappDeinit())
}

func (s *Subscriber) NotifyKillswitch(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesKillSwitchEnabledValue(data))
}

func (s *Subscriber) NotifyAccountCheck(any) error {
	return s.fetchSubscriptions()
}

func (s *Subscriber) NotifyAutoconnect(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesAutoConnectEnabledValue(data))
}

func (s *Subscriber) NotifyDefaults(any) error {
	return s.clearSubscriptions()
}

func (s *Subscriber) NotifyDNS(data events.DataDNS) error {
	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesCustomDnsEnabledMeta(fmt.Sprintf(`{"count":%d}`, len(data.Ips)))); err != nil {
		return err
	}
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesCustomDnsEnabledValue(len(data.Ips) > 0))
}

func (s *Subscriber) NotifyFirewall(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesFirewallEnabledValue(data))
}

func (s *Subscriber) NotifyRouting(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesRoutingEnabledValue(data))
}

func (s *Subscriber) NotifyLANDiscovery(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesLocalNetworkDiscoveryAllowedValue(data))
}

func (s *Subscriber) NotifyVirtualLocation(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesVirtualServerEnabledValue(data))
}

func (s *Subscriber) NotifyPostquantumVpn(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesPostQuantumEnabledValue(data))
}

func (s *Subscriber) NotifyIpv6(data bool) error {
	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateIpv6EnabledValue(data)); err != nil {
		return err
	}
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesIpv6EnabledValue(data))
}

func (s *Subscriber) NotifyLogin(data events.DataAuthorization) error {
	var eventTrigger moose.NordvpnappEventTrigger
	switch data.EventTrigger {
	case events.TriggerApp:
		eventTrigger = moose.NordvpnappEventTriggerApp
	case events.TriggerUser:
		eventTrigger = moose.NordvpnappEventTriggerUser
	default:
		eventTrigger = moose.NordvpnappEventTriggerApp
	}

	var eventStatus moose.NordvpnappEventStatus
	switch data.EventStatus {
	case events.StatusAttempt:
		eventStatus = moose.NordvpnappEventStatusAttempt
	case events.StatusSuccess:
		eventStatus = moose.NordvpnappEventStatusSuccess
	case events.StatusFailure:
		eventStatus = moose.NordvpnappEventStatusFailureDueToRuntimeException
	default:
		eventStatus = moose.NordvpnappEventStatusAttempt
	}

	// regular login, or login after signup
	mooseFn := moose.NordvpnappSendServiceQualityAuthorizationLogin
	if data.EventType == events.LoginSignUp {
		mooseFn = moose.NordvpnappSendServiceQualityAuthorizationRegister
	}

	if err := s.response(mooseFn(
		int32(data.DurationMs),
		eventTrigger,
		eventStatus,
		moose.NordvpnappOptBoolNone,
		-1,
		nil,
	)); err != nil {
		return err
	}

	if data.EventStatus == events.StatusSuccess {
		return s.fetchSubscriptions()
	}
	return nil
}

func (s *Subscriber) NotifyLogout(data events.DataAuthorization) error {
	var eventTrigger moose.NordvpnappEventTrigger
	switch data.EventTrigger {
	case events.TriggerApp:
		eventTrigger = moose.NordvpnappEventTriggerApp
	case events.TriggerUser:
		eventTrigger = moose.NordvpnappEventTriggerUser
	default:
		eventTrigger = moose.NordvpnappEventTriggerApp
	}

	var eventStatus moose.NordvpnappEventStatus
	switch data.EventStatus {
	case events.StatusAttempt:
		eventStatus = moose.NordvpnappEventStatusAttempt
	case events.StatusSuccess:
		eventStatus = moose.NordvpnappEventStatusSuccess
	case events.StatusFailure:
		eventStatus = moose.NordvpnappEventStatusFailureDueToRuntimeException
	default:
		eventStatus = moose.NordvpnappEventStatusAttempt
	}

	if err := s.response(moose.NordvpnappSendServiceQualityAuthorizationLogout(
		int32(data.DurationMs),
		eventTrigger,
		eventStatus,
		moose.NordvpnappOptBoolNone,
		-1,
		nil,
	)); err != nil {
		return err
	}

	if data.EventStatus == events.StatusSuccess {
		return s.clearSubscriptions()
	}
	return nil
}

func (s *Subscriber) NotifyMFA(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesMfaEnabledValue(data))
}

func (s *Subscriber) NotifyUiItemsClick(data events.UiItemsAction) error {
	itemType := moose.NordvpnappUserInterfaceItemTypeButton
	if data.ItemType == "textbox" {
		itemType = moose.NordvpnappUserInterfaceItemTypeTextBox
	}
	return s.response(moose.NordvpnappSendUserInterfaceUiItemsClick(
		data.ItemName,
		itemType,
		data.ItemValue,
		data.FormReference,
		nil,
	))
}

func (s *Subscriber) NotifyHeartBeat(period time.Duration) error {
	if err := s.response(moose.NordvpnappSendServiceQualityStatusHeartbeat(int32(period.Minutes()), nil)); err != nil {
		return err
	}
	if !s.initialHeartbeatSent {
		s.mux.Lock()
		defer s.mux.Unlock()
		s.initialHeartbeatSent = true
	}
	return nil
}

func (s *Subscriber) NotifyDeviceLocation(insights core.Insights) error {
	if err := s.response(moose.NordvpnappSetContextDeviceLocationCity(insights.City)); err != nil {
		return fmt.Errorf("setting moose device location city: %w", err)
	}
	if err := s.response(moose.NordvpnappSetContextDeviceLocationCountry(insights.Country)); err != nil {
		return fmt.Errorf("setting moose device location country: %w", err)
	}
	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateIspValue(insights.Isp)); err != nil {
		return fmt.Errorf("setting moose ISP value: %w", err)
	}
	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateIspAsnValue(strconv.Itoa(insights.IspAsn))); err != nil {
		return fmt.Errorf("setting moose ISP ASN value: %w", err)
	}
	return nil
}

func (s *Subscriber) NotifyNotify(bool) error { return nil }

func (s *Subscriber) NotifyMeshnet(data bool) error {
	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesMeshnetEnabledValue(data)); err != nil {
		return err
	}
	if s.initialHeartbeatSent {
		// 0 duration indicates that this is not a periodic heart beat
		return s.NotifyHeartBeat(time.Duration(0))
	}
	return nil
}

func (s *Subscriber) NotifyObfuscate(data bool) error {
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesObfuscationEnabledValue(data))
}

func (s *Subscriber) NotifyPeerUpdate([]string) error { return nil }

func (s *Subscriber) NotifySelfRemoved(any) error { return nil }

func (s *Subscriber) NotifyThreatProtectionLite(data bool) error {
	if s.connectionStartTime.IsZero() {
		if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateThreatProtectionLiteEnabledValue(data)); err != nil {
			return err
		}
	}
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesThreatProtectionLiteEnabledValue(data))
}

func (s *Subscriber) NotifyProtocol(data config.Protocol) error {
	var protocol moose.NordvpnappVpnConnectionProtocol
	switch data {
	case config.Protocol_UDP:
		protocol = moose.NordvpnappVpnConnectionProtocolUdp
	case config.Protocol_TCP:
		protocol = moose.NordvpnappVpnConnectionProtocolTcp
	case config.Protocol_UNKNOWN_PROTOCOL:
		fallthrough
	default:
		protocol = moose.NordvpnappVpnConnectionProtocolRecommended
	}
	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateProtocolValue(protocol)); err != nil {
		return err
	}
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesProtocolValue(protocol))
}

func (s *Subscriber) NotifyAllowlist(data events.DataAllowlist) error {
	enabled := len(data.UDPPorts) != 0 || len(data.TCPPorts) != 0 || len(data.Subnets) != 0
	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesSplitTunnelingEnabledMeta(
		fmt.Sprintf(`{"udp_ports":%d,"tcp_ports:%d,"subnets":%d}`, len(data.UDPPorts), len(data.TCPPorts), len(data.Subnets)),
	)); err != nil {
		return err
	}
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesSplitTunnelingEnabledValue(enabled))
}

func (s *Subscriber) NotifyTechnology(data config.Technology) error {
	var technology moose.NordvpnappVpnConnectionTechnology
	switch data {
	case config.Technology_NORDLYNX:
		technology = moose.NordvpnappVpnConnectionTechnologyNordlynx
	case config.Technology_OPENVPN:
		technology = moose.NordvpnappVpnConnectionTechnologyOpenvpn
	case config.Technology_NORDWHISPER:
		technology = moose.NordvpnappVpnConnectionTechnologyNordwhisper
	case config.Technology_UNKNOWN_TECHNOLOGY:
		return errors.New("unknown technology")
	default:
		technology = moose.NordvpnappVpnConnectionTechnologyRecommended
	}
	if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateTechnologyValue(technology)); err != nil {
		return err
	}
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigUserPreferencesTechnologyValue(technology))
}

func (s *Subscriber) NotifyConnect(data events.DataConnect) error {
	var eventStatus moose.NordvpnappEventStatus
	switch data.EventStatus {
	case events.StatusAttempt:
		eventStatus = moose.NordvpnappEventStatusAttempt
	case events.StatusSuccess:
		eventStatus = moose.NordvpnappEventStatusSuccess
		s.mux.Lock()
		s.connectionStartTime = time.Now()
		s.connectionToMeshnetPeer = data.IsMeshnetPeer
		s.mux.Unlock()
	case events.StatusFailure:
		eventStatus = moose.NordvpnappEventStatusFailureDueToRuntimeException
	case events.StatusCanceled:
		eventStatus = moose.NordvpnappEventStatusFailureDueToUserInterrupt
	default:
		eventStatus = moose.NordvpnappEventStatusAttempt
	}

	if data.IsMeshnetPeer {
		return s.response(moose.NordvpnappSendServiceQualityServersConnectToMeshnetDevice(
			int32(data.DurationMs),
			eventStatus,
			moose.NordvpnappEventTriggerUser,
			-1,
			-1,
			nil,
		))
	} else {
		var threatProtection moose.NordvpnappOptBool
		if data.ThreatProtectionLite {
			threatProtection = moose.NordvpnappOptBoolTrue
		} else {
			threatProtection = moose.NordvpnappOptBoolFalse
		}

		var protocol moose.NordvpnappVpnConnectionProtocol
		switch data.Protocol {
		case config.Protocol_TCP:
			protocol = moose.NordvpnappVpnConnectionProtocolTcp
		case config.Protocol_UDP:
			protocol = moose.NordvpnappVpnConnectionProtocolUdp
		case config.Protocol_Webtunnel:
			protocol = moose.NordvpnappVpnConnectionProtocolWebtunnel
		case config.Protocol_UNKNOWN_PROTOCOL:
			protocol = moose.NordvpnappVpnConnectionProtocolNone
		default:
			protocol = moose.NordvpnappVpnConnectionProtocolRecommended
		}

		var technology moose.NordvpnappVpnConnectionTechnology
		switch data.Technology {
		case config.Technology_OPENVPN:
			technology = moose.NordvpnappVpnConnectionTechnologyOpenvpn
		case config.Technology_NORDLYNX:
			technology = moose.NordvpnappVpnConnectionTechnologyNordlynx
		case config.Technology_NORDWHISPER:
			technology = moose.NordvpnappVpnConnectionTechnologyNordwhisper
		case config.Technology_UNKNOWN_TECHNOLOGY:
			technology = moose.NordvpnappVpnConnectionTechnologyNone
		default:
			technology = moose.NordvpnappVpnConnectionTechnologyRecommended
		}

		var server moose.NordvpnappServerListSource
		if data.ServerFromAPI {
			server = moose.NordvpnappServerListSourceRecommendedByApi
		} else {
			server = moose.NordvpnappServerListSourceLocallyCachedServerList
		}

		var rule moose.NordvpnappServerSelectionRule
		switch data.TargetServerSelection {
		default:
			rule = moose.NordvpnappServerSelectionRuleRecommended
		}
		if err := s.response(moose.NordvpnappSendServiceQualityServersConnect(
			int32(data.DurationMs),
			eventStatus,
			moose.NordvpnappEventTriggerUser,
			moose.NordvpnappVpnConnectionTriggerNone,
			moose.NordvpnappVpnConnectionPresetNone,
			rule,
			server,
			data.TargetServerGroup,
			data.TargetServerDomain,
			data.TargetServerIP,
			data.TargetServerCountry,
			data.TargetServerCity,
			protocol,
			technology,
			moose.NordvpnappServerTypeNone,
			threatProtection,
			-1,
			"",
			-1,
			nil,
		)); err != nil {
			return err
		}
		if data.EventStatus == events.StatusSuccess {
			if err := s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateThreatProtectionLiteEnabledValue(data.ThreatProtectionLite)); err != nil {
				return err
			}
			return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateIsOnVpnValue(true))
		}
		return nil
	}
}

func (s *Subscriber) NotifyDisconnect(data events.DataDisconnect) error {
	eventStatus := moose.NordvpnappEventStatusAttempt
	switch data.EventStatus {
	case events.StatusAttempt:
		eventStatus = moose.NordvpnappEventStatusAttempt
	case events.StatusSuccess:
		eventStatus = moose.NordvpnappEventStatusSuccess
	case events.StatusFailure:
		eventStatus = moose.NordvpnappEventStatusFailureDueToRuntimeException
	}

	s.mux.Lock()
	connectionTime := int32(time.Since(s.connectionStartTime).Seconds())
	if connectionTime <= 0 {
		connectionTime = -1
	}
	s.connectionStartTime = time.Time{}
	s.mux.Unlock()

	if s.connectionToMeshnetPeer {
		if err := s.response(moose.NordvpnappSendServiceQualityServersDisconnectFromMeshnetDevice(
			int32(data.Duration.Milliseconds()),
			eventStatus,
			moose.NordvpnappEventTriggerUser,
			connectionTime, // seconds
			-1,
			nil,
		)); err != nil {
			return err
		}
	} else {
		var technology moose.NordvpnappVpnConnectionTechnology
		switch data.Technology {
		case config.Technology_OPENVPN:
			technology = moose.NordvpnappVpnConnectionTechnologyOpenvpn
		case config.Technology_NORDLYNX:
			technology = moose.NordvpnappVpnConnectionTechnologyNordlynx
		case config.Technology_NORDWHISPER:
			technology = moose.NordvpnappVpnConnectionTechnologyNordwhisper
		case config.Technology_UNKNOWN_TECHNOLOGY:
			technology = moose.NordvpnappVpnConnectionTechnologyNone
		default:
			technology = moose.NordvpnappVpnConnectionTechnologyRecommended
		}

		var protocol moose.NordvpnappVpnConnectionProtocol
		switch data.Protocol {
		case config.Protocol_TCP:
			protocol = moose.NordvpnappVpnConnectionProtocolTcp
		case config.Protocol_UDP:
			protocol = moose.NordvpnappVpnConnectionProtocolUdp
		case config.Protocol_Webtunnel:
			protocol = moose.NordvpnappVpnConnectionProtocolWebtunnel
		case config.Protocol_UNKNOWN_PROTOCOL:
			protocol = moose.NordvpnappVpnConnectionProtocolNone
		default:
			protocol = moose.NordvpnappVpnConnectionProtocolRecommended
		}

		var server moose.NordvpnappServerListSource
		if data.ServerFromAPI {
			server = moose.NordvpnappServerListSourceRecommendedByApi
		} else {
			server = moose.NordvpnappServerListSourceLocallyCachedServerList
		}

		var rule moose.NordvpnappServerSelectionRule
		switch data.TargetServerSelection {
		default:
			rule = moose.NordvpnappServerSelectionRuleRecommended
		}

		var threatProtection moose.NordvpnappOptBool
		if data.ThreatProtectionLite {
			threatProtection = moose.NordvpnappOptBoolTrue
		} else {
			threatProtection = moose.NordvpnappOptBoolFalse
		}

		if err := s.response(moose.NordvpnappSendServiceQualityServersDisconnect(
			int32(data.Duration.Milliseconds()),
			eventStatus,
			// App should never disconnect from VPN by itself. It has to receive either
			// user command (logout, set defaults) or bet shut down.
			moose.NordvpnappEventTriggerUser,
			moose.NordvpnappVpnConnectionTriggerNone, // pass proper trigger
			moose.NordvpnappVpnConnectionPresetNone,
			rule,
			server,
			"",
			"",
			"",
			"",
			"",
			protocol,
			technology,
			moose.NordvpnappServerTypeNone,
			threatProtection,
			connectionTime, // seconds
			"",
			errToExceptionCode(data.Error),
			nil,
		)); err != nil {
			return err
		}
		if err := s.response(moose.NordvpnappUnsetContextApplicationNordvpnappConfigCurrentStateThreatProtectionLiteEnabledValue()); err != nil {
			return err
		}
	}
	return s.response(moose.NordvpnappSetContextApplicationNordvpnappConfigCurrentStateIsOnVpnValue(false))
}

func (s *Subscriber) NotifyRequestAPI(data events.DataRequestAPI) error {
	if data.Request == nil {
		return fmt.Errorf("request nil")
	}
	responseCode := 0
	if data.Response != nil {
		responseCode = data.Response.StatusCode
	}

	fn, err := pickNotifier(data.Request.URL.Path)
	if err != nil {
		return err
	}

	var eventStatus moose.NordvpnappEventStatus
	if data.Error != nil {
		eventStatus = moose.NordvpnappEventStatusSuccess
	} else {
		eventStatus = moose.NordvpnappEventStatusFailureDueToRuntimeException
	}
	return s.response(fn(
		int32(data.Duration.Milliseconds()),
		eventStatus,
		moose.NordvpnappEventTriggerApp,
		data.Request.URL.Host,
		int32(responseCode),
		data.Request.Proto,
		0,
		"",
		"",
		"",
		"",
		"",
		nil,
	))
}

func (s *Subscriber) fetchSubscriptions() error {
	if !s.enabled {
		return nil
	}
	var cfg config.Config
	if err := s.Config.Load(&cfg); err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	token := cfg.TokensData[cfg.AutoConnectData.ID].Token

	payments, err := s.SubscriptionAPI.Payments(token)
	if err != nil {
		return fmt.Errorf("fetching payments: %w", err)
	}

	orders, err := s.SubscriptionAPI.Orders(token)
	if err != nil {
		return fmt.Errorf("fetching orders: %w", err)
	}

	payment, ok := findPayment(payments)
	if !ok {
		return fmt.Errorf("no valid payments found for the user")
	}

	var orderErr error
	order, ok := findOrder(payment, orders)
	if !ok {
		orderErr = fmt.Errorf("no valid order was found for the payment")
	}

	if err := s.setSubscriptions(
		payment,
		order,
		countFunc(payments, isPaymentValid, 2),
	); err != nil {
		errors.Join(orderErr, fmt.Errorf("setting subscriptions: %w", err))
	}

	return orderErr
}

func findPayment(payments []core.PaymentResponse) (core.Payment, bool) {
	// Sort by CreatedAt descending
	slices.SortFunc(payments, func(a core.PaymentResponse, b core.PaymentResponse) int {
		return -a.Payment.CreatedAt.Compare(b.Payment.CreatedAt)
	})

	// Find first element matching criteria
	index := slices.IndexFunc(payments, isPaymentValid)
	if index < 0 {
		return core.Payment{}, false
	}

	return payments[index].Payment, true
}

func findOrder(p core.Payment, orders []core.Order) (core.Order, bool) {
	// Find order matching the payment
	if p.Subscription.MerchantID != 25 && p.Subscription.MerchantID != 3 {
		return core.Order{}, false
	}
	index := slices.IndexFunc(orders, func(o core.Order) bool {
		var cmpID int
		switch p.Subscription.MerchantID {
		case 3:
			cmpID = o.ID
		case 25:
			cmpID = o.RemoteID
		}
		return p.Payer.OrderID == cmpID
	})
	if index < 0 {
		return core.Order{}, false
	}

	return orders[index], true
}

func isPaymentValid(pr core.PaymentResponse) bool {
	p := pr.Payment
	return p.Status == "done" ||
		p.Status == "chargeback" ||
		p.Status == "refunded" ||
		p.Status == "partially_refunded" ||
		p.Status == "trial"
}

// countFunc returns a number of elements in slice matching criteria
func countFunc[S ~[]E, E any](s S, f func(E) bool, stopAt int) int {
	count := 0
	for _, e := range s {
		if f(e) {
			count++
		}
		if stopAt >= 0 && count >= stopAt {
			return count
		}
	}
	return count
}

func (s *Subscriber) setSubscriptions(
	payment core.Payment,
	order core.Order,
	validPaymentsCount int,
) error {
	var plan core.Plan
	if len(order.Plans) > 0 {
		plan = order.Plans[0]
	}
	for _, fn := range []func() uint32{
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStateActivationDate(payment.CreatedAt.Format(internal.YearMonthDateFormat))
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStateFrequencyInterval(payment.Subscription.FrequencyInterval)
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStateFrequencyUnit(payment.Subscription.FrequencyUnit)
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStateIsActive(order.Status == "active")
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStateIsNewCustomer(validPaymentsCount == 1)
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStateMerchantId(payment.Subscription.MerchantID)
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStatePaymentAmount(payment.Amount)
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStatePaymentCurrency(payment.Currency)
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStatePaymentProvider(payment.Provider)
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStatePaymentStatus(payment.Status)
		},
		func() uint32 {
			if plan.ID != 0 {
				return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStatePlanId(plan.ID)
			}
			return 0
		},
		func() uint32 {
			if plan.Type != "" {
				return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStatePlanType(plan.Type)
			}
			return 0
		},
		func() uint32 {
			return moose.NordvpnappSetContextUserNordvpnappSubscriptionCurrentStateSubscriptionStatus(payment.Subscription.Status)
		},
	} {
		if err := s.response(fn()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Subscriber) clearSubscriptions() error {
	for _, fn := range []func() uint32{
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStateActivationDate()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStateFrequencyInterval()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStateFrequencyUnit()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStateIsActive()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStateIsNewCustomer()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStateMerchantId()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStatePaymentAmount()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStatePaymentCurrency()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStatePaymentProvider()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStatePaymentStatus()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStatePlanId()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStatePlanType()
		},
		func() uint32 {
			return moose.NordvpnappUnsetContextUserNordvpnappSubscriptionCurrentStateSubscriptionStatus()
		},
	} {
		if err := s.response(fn()); err != nil {
			return err
		}
	}

	return nil
}

func (s *Subscriber) updateEventDomain() error {
	domainUrl, err := url.Parse(s.Domain)
	if err != nil {
		return err
	}
	// TODO: Remove subdomain handling logic as it brings no value after domain rotation removal
	if s.Subdomain != "" {
		domainUrl.Host = s.Subdomain + "." + domainUrl.Host
	}
	s.currentDomain = domainUrl.String()
	return nil
}

func DrainStart(dbPath string) uint32 {
	return worker.Start(
		dbPath,
		"http://localhost",
		100,
		1000,
		false,
		20,
		false,
	)
}

func errToExceptionCode(err error) int32 {
	if err == nil {
		return -1
	}
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "config"):
		return 1
	case strings.Contains(errStr, "networker"):
		return 2
	}
	return -1
}
