package main

import (
	"database/sql"
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

type GeoLocation struct {
	Country string `json:"country"`
	City    string `json:"city"`
}

// Structure flexible pour gérer différents formats de réponse API
type ApiResponse struct {
	AllTrips []Trip `json:"alltrips,omitempty"`
	Trips    []Trip `json:"trips,omitempty"`
	Data     []Trip `json:"data,omitempty"`
}

var cfg Config
var db *sql.DB

func main() {
	var err error
	db, err = initDB("stats.db")
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

	go startCacheResetter()

	http.HandleFunc("/", handler)
	http.HandleFunc("/stats", statsHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Printf("🚀 Redirector running on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func initDB(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", filepath)
	if err != nil {
		return nil, err
	}

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS visits (
		"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
		"url" TEXT,
		"timestamp" DATETIME,
		"country" TEXT,
		"city" TEXT
	);`

	_, err = db.Exec(createTableSQL)
	if err != nil {
		return nil, err
	}

	log.Println("✅ Database initialized and table created.")
	return db, nil
}

func handler(w http.ResponseWriter, r *http.Request) {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	if len(host) > 4 && host[:4] == "www." {
		host = host[4:]
	}

	username, ok := cfg.Domains[host]
	if !ok {
		log.Printf("❌ Unknown host: %s", host)
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
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	if len(trips) == 0 {
		log.Printf("↩️ No trips found for %s → redirect to profile", username)
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	selectedTrip := selectTrip(trips)
	if selectedTrip == nil {
		log.Printf("↩️ No suitable trip found for %s → redirect to profile", username)
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	target := fmt.Sprintf("https://polarsteps.com/%s/%d-%s", username, selectedTrip.ID, selectedTrip.Slug)

	cache.Lock()
	cache.store[host] = target
	cache.Unlock()

	log.Printf("➡️ Redirecting %s → %s", username, target)
	http.Redirect(w, r, target, http.StatusFound)
}

func getGeoLocation(ip string) (*GeoLocation, error) {
	if ip == "" || ip == "::1" || ip == "127.0.0.1" {
		return &GeoLocation{Country: "local", City: "localhost"}, nil
	}

	resp, err := http.Get("http://ip-api.com/json/" + ip)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var geo GeoLocation
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		return nil, err
	}
	return &geo, nil
}

func statsHandler(w http.ResponseWriter, r *http.Request) {
	type CountryStats struct {
		Total   int            `json:"total"`
		Details map[string]int `json:"countries"`
	}

	type StatsResponse struct {
		TotalVisits  int                       `json:"total_visits"`
		VisitsByUrl  map[string]CountryStats   `json:"visits_by_url"`
		UniqueUsers  int                       `json:"unique_users_today"`
	}

	rows, err := db.Query("SELECT url, country FROM visits")
	if err != nil {
		http.Error(w, "Failed to query stats", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	stats := make(map[string]map[string]int)
	totalVisits := 0
	for rows.Next() {
		var url, country string
		if err := rows.Scan(&url, &country); err != nil {
			log.Printf("⚠️ Error scanning row: %v", err)
			continue
		}
		totalVisits++
		if _, ok := stats[url]; !ok {
			stats[url] = make(map[string]int)
		}
		stats[url][country]++
	}

	visitsByUrl := make(map[string]CountryStats)
	for url, countryCounts := range stats {
		total := 0
		for _, count := range countryCounts {
			total += count
		}
		visitsByUrl[url] = CountryStats{
			Total:   total,
			Details: countryCounts,
		}
	}

	// Compter les visiteurs uniques pour la journée en cours
	var uniqueUsersToday int
	err = db.QueryRow("SELECT COUNT(DISTINCT city) FROM visits WHERE date(timestamp) = date('now')").Scan(&uniqueUsersToday)
	if err != nil {
		log.Printf("⚠️ Failed to query unique users: %v", err)
	}

	response := StatsResponse{
		TotalVisits: totalVisits,
		VisitsByUrl: visitsByUrl,
		UniqueUsers: uniqueUsersToday,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding stats: %v", err)
		http.Error(w, "Error encoding stats", http.StatusInternalServerError)
	}
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

func startCacheResetter() {
	log.Println("🕒 Daily cache reset scheduled.")
	go func() {
		for {
			now := time.Now()
			nextMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(24 * time.Hour)
			durationUntilMidnight := nextMidnight.Sub(now)

			time.Sleep(durationUntilMidnight)

			cache.Lock()
			cache.store = make(map[string]string)
			cache.Unlock()
			log.Println("✅ Cache has been reset at midnight.")
			time.Sleep(60 * time.Second)
		}
	}()
}