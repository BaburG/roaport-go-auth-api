package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type AppConfig struct {
	KeycloakURL    string
	Realm          string
	AdminUser      string
	AdminPass      string
	AdminClientID  string
	MobileClientID string
}

var config AppConfig

func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Error loading .env file")
	}

	config = AppConfig{
		KeycloakURL:    os.Getenv("KEYCLOAK_URL"),
		Realm:          os.Getenv("REALM"),
		AdminUser:      os.Getenv("ADMIN_USERNAME"),
		AdminPass:      os.Getenv("ADMIN_PASSWORD"),
		AdminClientID:  os.Getenv("ADMIN_CLIENT_ID"),
		MobileClientID: os.Getenv("MOBILE_CLIENT_ID"),
	}

	http.HandleFunc("/register", registerHandler)
	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/refresh", refreshHandler)

	fmt.Println("Server running on port 5000...")
	http.ListenAndServe(":5000", nil)
}

func registerHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Register request came!", config.KeycloakURL, config.Realm, "asdasd")
	if r.Method != "POST" {
		respondJSON(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	var req struct {
		FirstName   string `json:"firstName"`
		LastName    string `json:"lastName"`
		Email       string `json:"email"`
		PhoneNumber string `json:"phoneNumber"`
		Password    string `json:"password"`
	}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, false, "Invalid request body", nil)
		return
	}

	adminToken, err := getAdminToken()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, false, "Failed to obtain admin token", nil)
		return
	}

	userPayload := map[string]interface{}{
		"enabled":   true,
		"username":  req.Email,
		"email":     req.Email,
		"firstName": req.FirstName,
		"lastName":  req.LastName,
		"attributes": map[string]interface{}{
			"phone_number": req.PhoneNumber,
		},
		"credentials": []map[string]interface{}{
			{
				"type":      "password",
				"value":     req.Password,
				"temporary": false,
			},
		},
	}

	userJSON, _ := json.Marshal(userPayload)
	createReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/admin/realms/%s/users", config.KeycloakURL, config.Realm), strings.NewReader(string(userJSON)))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(createReq)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, false, "Failed to create user", nil)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(resp.Body)
		respondJSON(w, http.StatusBadRequest, false, "User creation failed", string(errorBody))
		return
	}

	tokenData, err := getUserToken(req.Email, req.Password)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, false, "User login after registration failed", nil)
		return
	}

	userID := getUserIDByEmail(req.Email)

	userData := map[string]interface{}{
		"id":        userID,
		"username":  req.Email,
		"email":     req.Email,
		"firstName": req.FirstName,
		"lastName":  req.LastName,
	}

	result := map[string]interface{}{
		"user":          userData,
		"access_token":  tokenData["access_token"],
		"refresh_token": tokenData["refresh_token"],
	}

	respondJSON(w, http.StatusCreated, true, "User registered successfully", result)
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		respondJSON(w, http.StatusMethodNotAllowed, false, "Method not allowed.", nil)
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, false, "Invalid request body.", nil)
		return
	}

	tokenData, err := getUserToken(req.Email, req.Password)
	if err != nil || tokenData["access_token"] == nil {
		respondJSON(w, http.StatusUnauthorized, false, "Invalid credentials.", nil)
		return
	}

	// 🔥 userID + detay çek
	adminToken, err := getAdminToken()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, false, "Failed to get admin token", nil)
		return
	}

	client := &http.Client{}
	reqURL := fmt.Sprintf("%s/admin/realms/%s/users?email=%s", config.KeycloakURL, config.Realm, url.QueryEscape(req.Email))
	getReq, _ := http.NewRequest("GET", reqURL, nil)
	getReq.Header.Add("Authorization", "Bearer "+adminToken)

	resp, err := client.Do(getReq)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, false, "Failed to fetch user data", nil)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var users []map[string]interface{}
	json.Unmarshal(body, &users)

	if len(users) == 0 {
		respondJSON(w, http.StatusNotFound, false, "User not found", nil)
		return
	}

	user := users[0]

	userData := map[string]interface{}{
		"id":        user["id"],
		"username":  user["username"],
		"email":     user["email"],
		"firstName": user["firstName"],
		"lastName":  user["lastName"],
	}

	result := map[string]interface{}{
		"user":          userData,
		"access_token":  tokenData["access_token"],
		"refresh_token": tokenData["refresh_token"],
	}

	respondJSON(w, http.StatusOK, true, "Login successful.", result)
}

func refreshHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		respondJSON(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	var req struct {
		RefreshToken string `json:"refreshToken"`
	}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil || req.RefreshToken == "" {
		respondJSON(w, http.StatusBadRequest, false, "Invalid refresh token", nil)
		return
	}

	form := url.Values{}
	form.Add("client_id", config.MobileClientID)
	form.Add("grant_type", "refresh_token")
	form.Add("refresh_token", req.RefreshToken)

	resp, err := http.Post(
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", config.KeycloakURL, config.Realm),
		"application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, false, "Refresh request failed", nil)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tokenData map[string]interface{}
	json.Unmarshal(body, &tokenData)

	if tokenData["access_token"] == nil {
		respondJSON(w, http.StatusUnauthorized, false, "Invalid refresh token", nil)
		return
	}

	result := map[string]interface{}{
		"access_token":  tokenData["access_token"],
		"refresh_token": tokenData["refresh_token"],
	}

	respondJSON(w, http.StatusOK, true, "Token refreshed", result)
}

func respondJSON(w http.ResponseWriter, statusCode int, status bool, message string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  status,
		"message": message,
		"data":    data,
	})
}

func getAdminToken() (string, error) {
	form := url.Values{}
	form.Add("client_id", config.AdminClientID)
	form.Add("grant_type", "password")
	form.Add("username", config.AdminUser)
	form.Add("password", config.AdminPass)

	resp, err := http.Post(fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", config.KeycloakURL, config.Realm), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tokenData map[string]interface{}
	json.Unmarshal(body, &tokenData)

	accessToken, ok := tokenData["access_token"].(string)
	if !ok {
		return "", fmt.Errorf("admin token not found")
	}
	return accessToken, nil
}

func getUserToken(email, password string) (map[string]interface{}, error) {
	form := url.Values{}
	form.Add("client_id", config.MobileClientID)
	form.Add("grant_type", "password")
	form.Add("username", email)
	form.Add("password", password)

	resp, err := http.Post(fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", config.KeycloakURL, config.Realm), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tokenData map[string]interface{}
	json.Unmarshal(body, &tokenData)

	return tokenData, nil
}

func getUserIDByEmail(email string) string {
	adminToken, err := getAdminToken()
	if err != nil {
		return ""
	}
	client := &http.Client{}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/admin/realms/%s/users?email=%s", config.KeycloakURL, config.Realm, url.QueryEscape(email)), nil)
	req.Header.Add("Authorization", "Bearer "+adminToken)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var users []map[string]interface{}
	json.Unmarshal(body, &users)

	if len(users) > 0 {
		return users[0]["id"].(string)
	}

	return ""
}
