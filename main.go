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
type ApiResponse struct {
	AllTrips []Trip `json:"alltrips"`
}

var cfg Config

func main() {
	yamlFile, _ := os.ReadFile("domains.yaml")
	_ = yaml.Unmarshal(yamlFile, &cfg)

	http.HandleFunc("/", handler)
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	log.Printf("🚀 Redirector running on :%s\n", port)
	http.ListenAndServe(":"+port, nil)
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
	url := fmt.Sprintf("%s/users/byusername/%s", API_URL, username)

	resp, err := http.Get(url)
	if err != nil {
		log.Printf("⚠️ API request failed for %s: %v", username, err)
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}
	defer resp.Body.Close()

	var data ApiResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Printf("⚠️ Failed to parse API response for %s", username)
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	now := time.Now().Unix()
	var current *Trip
	var future, past []Trip

	for _, t := range data.AllTrips {
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

	sort.Slice(future, func(i, j int) bool { return future[i].StartDate < future[j].StartDate })
	sort.Slice(past, func(i, j int) bool { return *past[i].EndDate > *past[j].EndDate })

	var sel *Trip
	if current != nil {
		sel = current
	} else if len(future) > 0 {
		sel = &future[0]
	} else if len(past) > 0 {
		sel = &past[0]
	}

	if sel != nil {
		target := fmt.Sprintf("https://polarsteps.com/%s/%d-%s", username, sel.ID, sel.Slug)
		log.Printf("➡️ Redirecting %s → %s", username, target)
		http.Redirect(w, r, target, http.StatusFound)
	} else {
		log.Printf("↩️ No trips found for %s → redirect to profile", username)
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
	}
}
