package main

import (
	"bytes"
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

type DomainConfig struct {
	Username string `yaml:"username"`
	GAConfig *GAConfig `yaml:"ga,omitempty"`
}

type GAConfig struct {
	MeasurementID string `yaml:"measurement_id"`
	SecretKey     string `yaml:"secret_key"`
}

type Config struct {
	Domains map[string]DomainConfig `yaml:"domains"`
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

	// Charger les configurations GA depuis les variables d'environnement
	loadGAConfigFromEnv()

	http.HandleFunc("/", handler)
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	
	log.Printf("🚀 Redirector running on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// Charge les configurations GA depuis les variables d'environnement
func loadGAConfigFromEnv() {
	for host, domainConfig := range cfg.Domains {
		// Normaliser le nom de l'host pour les variables d'environnement
		envPrefix := normalizeHostForEnv(host)
		
		measurementID := os.Getenv(envPrefix + "_GA_MEASUREMENT_ID")
		secretKey := os.Getenv(envPrefix + "_GA_SECRET_KEY")
		
		// Si les variables d'env sont absentes, utiliser le YAML
		if measurementID == "" && domainConfig.GAConfig != nil {
			measurementID = domainConfig.GAConfig.MeasurementID
			secretKey = domainConfig.GAConfig.SecretKey
		}
		
		if measurementID != "" && secretKey != "" {
			if domainConfig.GAConfig == nil {
				domainConfig.GAConfig = &GAConfig{}
			}
			domainConfig.GAConfig.MeasurementID = measurementID
			domainConfig.GAConfig.SecretKey = secretKey
			
			// Mettre à jour la configuration
			cfg.Domains[host] = domainConfig
			
			log.Printf("✅ GA configuration loaded for %s (ID: %s)", host, measurementID)
		} else {
			log.Printf("⚠️ No GA configuration found for %s", host)
		}
	}
}

// Normalise un nom d'host pour les variables d'environnement
// Exemple: whereisanthony.com -> WHEREISANTHONY_COM
func normalizeHostForEnv(host string) string {
	normalized := ""
	for _, char := range host {
		if char >= 'a' && char <= 'z' {
			normalized += string(char - 32) // Convertir en majuscule
		} else if char >= 'A' && char <= 'Z' {
			normalized += string(char)
		} else if char >= '0' && char <= '9' {
			normalized += string(char)
		} else {
			normalized += "_"
		}
	}
	return normalized
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

	domainConfig, ok := cfg.Domains[host]
	if !ok {
		log.Printf("❌ Unknown host: %s", host)
		http.NotFound(w, r)
		return
	}

	username := domainConfig.Username
	log.Printf("🌍 Request from host=%s → username=%s", host, username)

	// Récupérer les voyages de l'utilisateur
	trips, err := fetchUserTrips(username)
	if err != nil {
		log.Printf("⚠️ Failed to fetch trips for %s: %v", username, err)
		// Envoyer un événement GA d'erreur si configuré
		if domainConfig.GAConfig != nil {
			sendGAEvent(domainConfig.GAConfig, "error", map[string]interface{}{
				"error_type": "api_fetch_failed",
				"username":   username,
			})
		}
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	if len(trips) == 0 {
		log.Printf("↩️ No trips found for %s → redirect to profile", username)
		// Envoyer un événement GA si configuré
		if domainConfig.GAConfig != nil {
			sendGAEvent(domainConfig.GAConfig, "redirect", map[string]interface{}{
				"redirect_type": "no_trips",
				"username":      username,
				"destination":   "profile",
			})
		}
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	// Sélectionner le voyage approprié
	selectedTrip := selectTrip(trips)
	if selectedTrip == nil {
		log.Printf("↩️ No suitable trip found for %s → redirect to profile", username)
		// Envoyer un événement GA si configuré
		if domainConfig.GAConfig != nil {
			sendGAEvent(domainConfig.GAConfig, "redirect", map[string]interface{}{
				"redirect_type": "no_suitable_trip",
				"username":      username,
				"destination":   "profile",
			})
		}
		http.Redirect(w, r, "https://polarsteps.com/"+username, http.StatusFound)
		return
	}

	target := fmt.Sprintf("https://polarsteps.com/%s/%d-%s", username, selectedTrip.ID, selectedTrip.Slug)
	log.Printf("➡️ Redirecting %s → %s", username, target)
	
	// Envoyer un événement GA de redirection réussie si configuré
	if domainConfig.GAConfig != nil {
		sendGAEvent(domainConfig.GAConfig, "redirect", map[string]interface{}{
			"redirect_type": "trip",
			"username":      username,
			"trip_id":       selectedTrip.ID,
			"trip_slug":     selectedTrip.Slug,
			"destination":   "trip",
		})
	}
	
	http.Redirect(w, r, target, http.StatusFound)
}

// Envoie un événement à Google Analytics 4
func sendGAEvent(gaConfig *GAConfig, eventName string, parameters map[string]interface{}) {
	if gaConfig == nil || gaConfig.MeasurementID == "" || gaConfig.SecretKey == "" {
		return
	}
	
	// Construire le payload pour GA4 Measurement Protocol
	payload := map[string]interface{}{
		"client_id": generateClientID(),
		"events": []map[string]interface{}{
			{
				"name":       eventName,
				"params":     parameters,
			},
		},
	}
	
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		log.Printf("⚠️ Failed to marshal GA payload: %v", err)
		return
	}
	
	// URL de l'API GA4 Measurement Protocol
	url := fmt.Sprintf("https://www.google-analytics.com/mp/collect?measurement_id=%s&api_secret=%s", 
		gaConfig.MeasurementID, gaConfig.SecretKey)
	
	// Envoyer la requête en arrière-plan
	go func() {
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(payloadJSON))
		if err != nil {
			log.Printf("⚠️ Failed to send GA event: %v", err)
			return
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			log.Printf("⚠️ GA API returned status: %d", resp.StatusCode)
		} else {
			log.Printf("📊 GA event sent: %s", eventName)
		}
	}()
}

// Génère un client_id simple pour GA
func generateClientID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
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