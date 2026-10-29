package subscription

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/config"
)

type UpdateCallback func(endpoints []Endpoint) error

type Manager struct {
	cfg        *config.Config
	client     *http.Client
	mu         sync.RWMutex
	endpoints  []Endpoint
	lastHash   string
	lastPoll   time.Time
	pollErrors int64
	onUpdate   UpdateCallback
	aliveCheck func() bool
}

func NewManager(cfg *config.Config, onUpdate UpdateCallback, aliveCheck ...func() bool) *Manager {
	m := &Manager{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		onUpdate: onUpdate,
	}
	if len(aliveCheck) > 0 && aliveCheck[0] != nil {
		m.aliveCheck = aliveCheck[0]
	}
	return m
}

func (m *Manager) SetAliveCheck(check func() bool) {
	m.aliveCheck = check
}

func (m *Manager) GetEndpoints() []Endpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]Endpoint, len(m.endpoints))
	copy(res, m.endpoints)
	return res
}

func (m *Manager) GetLastPollTime() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastPoll
}

func (m *Manager) GetPollErrors() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pollErrors
}

func (m *Manager) Start(ctx context.Context) error {
	// 1. Initial poll (or load cache)
	if err := m.Poll(ctx); err != nil {
		log.Printf("[subscription] initial poll failed: %v, attempting to load from cache %s", err, m.cfg.SubscriptionCacheFile)
		if loadErr := m.loadFromCache(); loadErr != nil {
			log.Printf("[subscription] failed to load from cache: %v", loadErr)
		}
	}

	// 2. Start polling loop
	go func() {
		ticker := time.NewTicker(m.cfg.SubscriptionPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.Poll(ctx); err != nil {
					log.Printf("[subscription] poll failed: %v", err)
				}
			}
		}
	}()

	return nil
}

func (m *Manager) Poll(ctx context.Context) error {
	if m.cfg.SubscriptionURL == "" {
		// If subscription URL is not set, try loading cache
		return m.loadFromCache()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.SubscriptionURL, nil)
	if err != nil {
		m.incrementError()
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "xray-endpoints-prober/1.0")

	resp, err := m.client.Do(req)
	if err != nil {
		m.incrementError()
		// Try fallback to cache
		_ = m.loadFromCache()
		return fmt.Errorf("fetch subscription failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		m.incrementError()
		_ = m.loadFromCache()
		return fmt.Errorf("subscription HTTP status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		m.incrementError()
		return fmt.Errorf("read subscription body failed: %w", err)
	}

	endpoints, totalCount, excludedCount, hash, err := ParseSubscriptionContent(data, m.cfg.ProtocolsExclude)
	if err != nil {
		m.incrementError()
		return fmt.Errorf("parse subscription failed: %w", err)
	}

	m.mu.Lock()
	m.lastPoll = time.Now()
	changed := (hash != m.lastHash)
	processDead := (m.aliveCheck != nil && !m.aliveCheck())
	if changed {
		m.lastHash = hash
		m.endpoints = endpoints
	}
	m.mu.Unlock()

	log.Printf("[subscription] sync complete: total parsed: %d, included (active): %d, excluded by policy: %d",
		totalCount, len(endpoints), excludedCount)

	// Save to cache file
	if m.cfg.SubscriptionCacheFile != "" {
		_ = os.MkdirAll(filepath.Dir(m.cfg.SubscriptionCacheFile), 0755)
		_ = os.WriteFile(m.cfg.SubscriptionCacheFile, data, 0644)
	}

	if (changed || processDead) && m.onUpdate != nil {
		if processDead && !changed {
			log.Printf("[subscription] Xray process is down, re-triggering onUpdate with %d endpoints...", len(endpoints))
		} else {
			log.Printf("[subscription] detected subscription change (%d endpoints), updating Xray config and prober...", len(endpoints))
		}
		if err := m.onUpdate(endpoints); err != nil {
			log.Printf("[subscription] onUpdate callback error: %v", err)
		}
	}

	return nil
}

func (m *Manager) loadFromCache() error {
	if m.cfg.SubscriptionCacheFile == "" {
		return fmt.Errorf("no cache file configured")
	}

	data, err := os.ReadFile(m.cfg.SubscriptionCacheFile)
	if err != nil {
		return fmt.Errorf("reading cache file %s: %w", m.cfg.SubscriptionCacheFile, err)
	}

	endpoints, totalCount, excludedCount, hash, err := ParseSubscriptionContent(data, m.cfg.ProtocolsExclude)
	if err != nil {
		return fmt.Errorf("parsing cached content: %w", err)
	}

	m.mu.Lock()
	changed := (hash != m.lastHash)
	m.lastHash = hash
	m.endpoints = endpoints
	m.mu.Unlock()

	log.Printf("[subscription] loaded from cache: total parsed: %d, included (active): %d, excluded by policy: %d (file: %s)",
		totalCount, len(endpoints), excludedCount, m.cfg.SubscriptionCacheFile)

	if changed && m.onUpdate != nil {
		if err := m.onUpdate(endpoints); err != nil {
			log.Printf("[subscription] onUpdate callback error: %v", err)
		}
	}

	return nil
}

func (m *Manager) incrementError() {
	m.mu.Lock()
	m.pollErrors++
	m.mu.Unlock()
}
