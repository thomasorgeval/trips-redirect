package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const API_URL = "https://api.polarsteps.com"

var cache = struct {
	sync.RWMutex
	store map[string]string
}{store: make(map[string]string)}


type Config struct {
	Domains map[string]string `yaml:"domains"`
}

type Trip struct {
	ID        int    `json:"id"`
	Slug      string `json:"slug"`
	StartDate int64  `json:"start_date"`
	EndDate   *int64 `json:"end_date"`
}

// Structure flexible pour gérer différents formats de réponse API
type ApiResponse struct {
	AllTrips []Trip `json:"alltrips,omitempty"`
	Trips    []Trip `json:"trips,omitempty"`
	Data     []Trip `json:"data,omitempty"`
}

// Rybbit Analytics structures
type EventType string

const (
	EventPageview    EventType = "pageview"
	EventCustom      EventType = "custom_event"
	EventPerformance EventType = "performance"
	EventOutbound    EventType = "outbound"
	EventError       EventType = "error"
)

type RybbitEvent struct {
	Type         EventType `json:"type"`
	SiteID       string    `json:"site_id,omitempty"`
	Pathname     string    `json:"pathname,omitempty"`
	Hostname     string    `json:"hostname,omitempty"`
	PageTitle    string    `json:"page_title,omitempty"`
	Referrer     string    `json:"referrer,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	UserAgent    string    `json:"user_agent,omitempty"`
	IPAddress    string    `json:"ip_address,omitempty"`
	QueryString  string    `json:"querystring,omitempty"`
	Language     string    `json:"language,omitempty"`
	ScreenWidth  int       `json:"screenWidth,omitempty"`
	ScreenHeight int       `json:"screenHeight,omitempty"`
}

type RybbitConfig struct {
	APIKey  string
	APIURL  string
	SiteID  string
	Enabled bool
}

var cfg Config
var rybbitCfg RybbitConfig

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "stats.db"
	}

	var err error
	db, err = initDB(dbPath)
	if err != nil {
		log.Fatal("❌ Cannot initialize database:", err)
	}
	defer db.Close()

	yamlFile, err := os.ReadFile("domains.yaml")
	if err != nil {
		log.Fatal("❌ Cannot read domains.yaml:", err)
	}

	if err := yaml.Unmarshal(yamlFile, &cfg); err != nil {
		log.Fatal("❌ Cannot parse domains.yaml:", err)
	}

	// Initialize Rybbit configuration
	initRybbitConfig()

	http.HandleFunc("/", handler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Printf("🚀 Redirector running on :%s\n", port)
	if rybbitCfg.Enabled {
		log.Printf("📊 Rybbit Analytics enabled (Site ID: %s)\n", rybbitCfg.SiteID)
	}
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// Initialize Rybbit configuration from environment variables
func initRybbitConfig() {
	rybbitCfg.APIKey = os.Getenv("RYBBIT_API_KEY")
	rybbitCfg.APIURL = os.Getenv("RYBBIT_API_URL")
	rybbitCfg.SiteID = os.Getenv("RYBBIT_SITE_ID")

	// Enable Rybbit only if all required variables are set
	rybbitCfg.Enabled = rybbitCfg.APIKey != "" && rybbitCfg.APIURL != "" && rybbitCfg.SiteID != ""

	if !rybbitCfg.Enabled && (rybbitCfg.APIKey != "" || rybbitCfg.APIURL != "" || rybbitCfg.SiteID != "") {
		log.Println("⚠️ Rybbit Analytics partially configured - analytics disabled. Ensure RYBBIT_API_KEY, RYBBIT_API_URL, and RYBBIT_SITE_ID are all set.")
	}
}

// Send event to Rybbit Analytics
func sendRybbitEvent(event RybbitEvent) {
	if !rybbitCfg.Enabled {
		return
	}

	// Set default site_id if not provided
	if event.SiteID == "" {
		event.SiteID = rybbitCfg.SiteID
	}

	// Send event asynchronously to avoid blocking the response
	go func() {
		payload, err := json.Marshal(event)
		if err != nil {
			log.Printf("⚠️ Failed to marshal Rybbit event: %v", err)
			return
		}

		req, err := http.NewRequest("POST", rybbitCfg.APIURL, bytes.NewBuffer(payload))
		if err != nil {
			log.Printf("⚠️ Failed to create Rybbit request: %v", err)
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+rybbitCfg.APIKey)

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("⚠️ Failed to send Rybbit event: %v", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
			log.Printf("⚠️ Rybbit API returned status %d", resp.StatusCode)
		}
	}()
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	// Supprimer le préfixe www. si présent
	if len(host) > 4 && host[:4] == "www." {
		host = host[4:]
	}

	username, ok := cfg.Domains[host]
	if !ok {
		log.Printf("❌ Unknown host: %s", host)

		// Track 404 error
		sendRybbitEvent(RybbitEvent{
			Type:      EventError,
			Hostname:  host,
			Pathname:  r.URL.Path,
			UserAgent: r.Header.Get("User-Agent"),
			IPAddress: getClientIP(r),
			Referrer:  r.Header.Get("Referer"),
		})

		http.NotFound(w, r)
		return
	}

	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	if realIP, _, err := net.SplitHostPort(ip); err == nil {
		ip = realIP
	}

	geo, err := getGeoLocation(ip)
	if err != nil {
		log.Printf("⚠️ Could not get geolocation for IP %s: %v", ip, err)
	}

	go func() {
		country, city := "unknown", "unknown"
		if geo != nil {
			country = geo.Country
			city = geo.City
			log.Printf("🌍 Request from host=%s → username=%s, location=%s, %s", host, username, city, country)
		} else {
			log.Printf("🌍 Request from host=%s → username=%s", host, username)
		}

		_, err := db.Exec("INSERT INTO visits (url, timestamp, country, city) VALUES (?, ?, ?, ?)", host, time.Now(), country, city)
		if err != nil {
			log.Printf("⚠️ Failed to record visit for %s: %v", host, err)
		}
	}()

	cache.RLock()
	cachedURL, found := cache.store[host]
	cache.RUnlock()

	if found {
		log.Printf("✅ Cache hit for %s → %s", host, cachedURL)
		http.Redirect(w, r, cachedURL, http.StatusFound)
		return
	}

	log.Printf("❌ Cache miss for %s", host)

	trips, err := fetchUserTrips(username)
	if err != nil {
		log.Printf("⚠️ Failed to fetch trips for %s: %v", username, err)

		// Track API error
		sendRybbitEvent(RybbitEvent{
			Type:      EventError,
			Hostname:  host,
			Pathname:  r.URL.Path,
			UserAgent: r.Header.Get("User-Agent"),
			IPAddress: getClientIP(r),
			Referrer:  r.Header.Get("Referer"),
			UserID:    username,
		})

		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	if len(trips) == 0 {
		log.Printf("↩️ No trips found for %s → redirect to profile", username)

		// Track redirect to profile
		sendRybbitEvent(RybbitEvent{
			Type:      EventPageview,
			Hostname:  host,
			Pathname:  r.URL.Path,
			UserAgent: r.Header.Get("User-Agent"),
			IPAddress: getClientIP(r),
			Referrer:  r.Header.Get("Referer"),
			UserID:    username,
		})

		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	selectedTrip := selectTrip(trips)
	if selectedTrip == nil {
		log.Printf("↩️ No suitable trip found for %s → redirect to profile", username)

		// Track redirect to profile
		sendRybbitEvent(RybbitEvent{
			Type:      EventPageview,
			Hostname:  host,
			Pathname:  r.URL.Path,
			UserAgent: r.Header.Get("User-Agent"),
			IPAddress: getClientIP(r),
			Referrer:  r.Header.Get("Referer"),
			UserID:    username,
		})

		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	target := fmt.Sprintf("https://polarsteps.com/%s/%d-%s", username, selectedTrip.ID, selectedTrip.Slug)

	cache.Lock()
	cache.store[host] = target
	cache.Unlock()

	log.Printf("➡️ Redirecting %s → %s", username, target)

	// Track successful redirect as outbound link
	sendRybbitEvent(RybbitEvent{
		Type:      EventOutbound,
		Hostname:  host,
		Pathname:  r.URL.Path,
		PageTitle: fmt.Sprintf("Trip: %s", selectedTrip.Slug),
		UserAgent: r.Header.Get("User-Agent"),
		IPAddress: getClientIP(r),
		Referrer:  r.Header.Get("Referer"),
		UserID:    username,
	})

	http.Redirect(w, r, target, http.StatusFound)
}

// Get client IP address from request
func getClientIP(r *http.Request) string {
	// Try X-Forwarded-For header first (for proxies/load balancers)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if idx := bytes.IndexByte([]byte(xff), ','); idx > 0 {
			return xff[:idx]
		}
		return xff
	}

	// Try X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	return r.RemoteAddr
}

func fetchUserTrips(username string) ([]Trip, error) {
	url := fmt.Sprintf("%s/users/byusername/%s", API_URL, username)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	// D'abord, lire la réponse brute pour le débogage
	var rawResponse map[string]interface{}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&rawResponse); err != nil {
		return nil, fmt.Errorf("failed to decode JSON: %w", err)
	}

	// Convertir en JSON puis décoder avec notre structure
	jsonData, err := json.Marshal(rawResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	var data ApiResponse
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to ApiResponse: %w", err)
	}

	// Essayer différents champs pour les voyages
	var trips []Trip
	if len(data.AllTrips) > 0 {
		trips = data.AllTrips
	} else if len(data.Trips) > 0 {
		trips = data.Trips
	} else if len(data.Data) > 0 {
		trips = data.Data
	} else {
		// Essayer de chercher dans la réponse brute
		if tripsInterface, ok := rawResponse["trips"]; ok {
			if tripsData, err := json.Marshal(tripsInterface); err == nil {
				json.Unmarshal(tripsData, &trips)
			}
		}
	}

	log.Printf("📊 Found %d trips for %s", len(trips), username)
	return trips, nil
}

func selectTrip(trips []Trip) *Trip {
	now := time.Now().Unix()
	var current *Trip
	var future, past []Trip

	for _, t := range trips {
		if t.StartDate <= now && (t.EndDate == nil || *t.EndDate >= now) {
			current = &t
			break
		}
		if t.StartDate > now {
			future = append(future, t)
		}
		if t.EndDate != nil && *t.EndDate < now {
			past = append(past, t)
		}
	}

	// Trier les voyages futurs par date de début (plus proche en premier)
	sort.Slice(future, func(i, j int) bool {
		return future[i].StartDate < future[j].StartDate
	})

	// Trier les voyages passés par date de fin (plus récent en premier)
	sort.Slice(past, func(i, j int) bool {
		return *past[i].EndDate > *past[j].EndDate
	})

	// Priorité : voyage en cours > voyage futur le plus proche > voyage passé le plus récent
	if current != nil {
		return current
	} else if len(future) > 0 {
		return &future[0]
	} else if len(past) > 0 {
		return &past[0]
	}

	return nil
}

// Fonction utilitaire pour obtenir les clés d'une map
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
