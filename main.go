package main

import (
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"log"
	"net/http"
	"os"
	"sort"
	"time"
)

const API_URL = "https://api.polarsteps.com"

type Config struct {
	Domains map[string]string `yaml:"domains"`
}

type Trip struct {
	ID        int     `json:"id"`
	Slug      string  `json:"slug"`
	StartDate int64   `json:"start_date"`
	EndDate   *int64  `json:"end_date"`
}

// Structure flexible pour gérer différents formats de réponse API
type ApiResponse struct {
	AllTrips []Trip `json:"alltrips,omitempty"`
	Trips    []Trip `json:"trips,omitempty"`
	Data     []Trip `json:"data,omitempty"`
}

var cfg Config

func main() {
	yamlFile, err := os.ReadFile("domains.yaml")
	if err != nil {
		log.Fatal("❌ Cannot read domains.yaml:", err)
	}
	
	if err := yaml.Unmarshal(yamlFile, &cfg); err != nil {
		log.Fatal("❌ Cannot parse domains.yaml:", err)
	}

	http.HandleFunc("/", handler)
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	
	log.Printf("🚀 Redirector running on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handler(w http.ResponseWriter, r *http.Request) {
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
		http.NotFound(w, r)
		return
	}

	log.Printf("🌍 Request from host=%s → username=%s", host, username)

	// Récupérer les voyages de l'utilisateur
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

	// Sélectionner le voyage approprié
	selectedTrip := selectTrip(trips)
	if selectedTrip == nil {
		log.Printf("↩️ No suitable trip found for %s → redirect to profile", username)
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	target := fmt.Sprintf("https://polarsteps.com/%s/%d-%s", username, selectedTrip.ID, selectedTrip.Slug)
	log.Printf("➡️ Redirecting %s → %s", username, target)
	http.Redirect(w, r, target, http.StatusFound)
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