package main

import (
	"context"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/ory-am/hydra/sdk"
	"github.com/ory/common/env"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"html/template"
	"log"
	"net/http"
	"os"
)

// This store will be used to save user authentication
var store = sessions.NewCookieStore([]byte("something-very-secret-keep-it-safe"))

const (

	// The session is a unique session identifier
	sessionName = "authentication"

	// A state for performing the OAuth 2.0 flow. This is usually not part of a consent app, but in order for the demo
	// to make sense, it performs the OAuth 2.0 authorize code flow.
	state = "demostatedemostatedemo"
)

// This is the Hydra SDK
var client *sdk.Client

var consentURL = "http://localhost:4444"
var consentPort = "4445"
var hydraURL = "http://localhost:4445"

func main() {
	var err error

	consentURL = os.Getenv("CONSENT_URL")
	consentPort = os.Getenv("CONSENT_PORT")

	// Initialize the hydra SDK. The defaults work if you started hydra as described in the README.md
	client, err = sdk.Connect(
		sdk.ClientID(env.Getenv("HYDRA_CLIENT_ID", os.Getenv("HYDRA_CLIENT_ID"))),
		sdk.ClientSecret(env.Getenv("HYDRA_CLIENT_SECRET", os.Getenv("HYDRA_CLIENT_SECRET"))),
		sdk.ClusterURL(env.Getenv("HYDRA_CLUSTER_URL", os.Getenv("HYDRA_URL"))),
	)
	if err != nil {
		panic(err)
	}

	// Set up a router and some routes
	r := mux.NewRouter()
	r.HandleFunc("/", handleHome)
	r.HandleFunc("/consent", handleConsent)
	r.HandleFunc("/login", handleLogin)
	r.HandleFunc("/callback", handleCallback)

	// Start http server
	log.Printf("Listening to %s", consentURL)
	err = http.ListenAndServe(fmt.Sprintf(":%s", consentPort), r)
	if err != nil {
		panic(err)
	}

}

// handles request at /home - a small page that let's you know what you can do in this app. Usually the first.
// page a user sees.
func handleHome(w http.ResponseWriter, _ *http.Request) {
	log.Printf("handleHome")
	var authUrl = client.OAuth2Config(fmt.Sprintf("%s/callback", consentURL), "offline", "openid").AuthCodeURL(state) + "&nonce=" + state
	renderTemplate(w, "home.html", authUrl)
}

// After pressing "click here", the Authorize Code flow is performed and the user is redirected to Hydra. Next, Hydra
// generates the consent challenge and redirects us to the consent endpoint which we set with `CONSENT_URL=http://host:port/consent`.
func handleConsent(w http.ResponseWriter, r *http.Request) {
	log.Printf("handleConsent")
	// Get the challenge from the query.
	challenge := r.URL.Query().Get("challenge")
	if challenge == "" {
		http.Error(w, errors.New("Consent endpoint was called without a consent challenge").Error(), http.StatusBadRequest)
		return
	}

	// Verify the challenge and extract the challenge claims.
	claims, err := client.Consent.VerifyChallenge(challenge)
	if err != nil {
		http.Error(w, errors.Wrap(err, "The consent challenge could not be verified").Error(), http.StatusBadRequest)
		return
	}

	// This little helper checks if our user is already authenticated. If not, we will redirect him to the login endpoint.
	user := authenticated(r)
	if user == "" {
		http.Redirect(w, r, "/login?challenge="+challenge, http.StatusFound)
		return
	}

	// Apparently, the user is logged in. Now we check if we received POST request, or a GET request.
	if r.Method == "POST" {
		// Ok, apparently the user gave his consent!

		// Parse the HTTP form - required by Go.
		if err := r.ParseForm(); err != nil {
			http.Error(w, errors.Wrap(err, "Could not parse form").Error(), http.StatusBadRequest)
			return
		}

		// Let's check which scopes the user granted.
		var grantedScopes = []string{}
		for key := range r.PostForm {
			// And add each scope to the list of granted scopes.
			grantedScopes = append(grantedScopes, key)
		}

		// Ok, now we generate the challenge response.
		redirectUrl, err := client.Consent.GenerateResponse(&sdk.ResponseRequest{
			// We need to include the original challenge.
			Challenge: challenge,

			// The subject is a string, usually the user id.
			Subject: user,

			// The scopes our user granted.
			Scopes: grantedScopes,

			// Data that will be available on the token introspection and warden endpoints.
			AccessTokenExtra: struct {
				Foo string `json:"foo"`
			}{Foo: "foo"},

			// If we issue an ID token, we can set extra data for that id token here.
			IDTokenExtra: struct {
				Bar string `json:"bar"`
			}{Bar: "bar"},
		})
		if err != nil {
			http.Error(w, errors.Wrap(err, "Could not sign the consent challenge").Error(), http.StatusBadRequest)
			return
		}

		// Redirect the user back to hydra, and append the consent response! If the user denies request you can
		// either handle the error in the authentication endpoint, or redirect the user back to the original application
		// with:
		//
		//   redirectUrl, _ := c.DenyConsent(challenge)
		http.Redirect(w, r, redirectUrl, http.StatusFound)
		return
	}

	// We received a get request, so let's show the html site where the user gives his consent.
	renderTemplate(w, "consent.html", struct {
		*sdk.ChallengeClaims
		Challenge string
	}{ChallengeClaims: claims, Challenge: challenge})
}

// The user hits this endpoint if he is not authenticated.

func handleLogin(w http.ResponseWriter, r *http.Request) {
	log.Printf("handleLogin")

	challenge := r.URL.Query().Get("challenge")

	// Is it a POST request?
	if r.Method == "POST" {
		// Parse the form
		if err := r.ParseForm(); err != nil {
			http.Error(w, errors.Wrap(err, "Could not parse form").Error(), http.StatusBadRequest)
			return
		}

		// Check the user's credentials
		if r.Form.Get("username") != r.Form.Get("password") {
			http.Error(w, "Provided credentials are wrong, try anything where password = username", http.StatusBadRequest)
			return
		}

		// Let's create a session where we store the user id. We can ignore errors from the session store
		// as it will always return a session!
		session, _ := store.Get(r, sessionName)
		session.Values["user"] = r.Form.Get("username")

		// Store the session in the cookie
		if err := store.Save(r, w, session); err != nil {
			http.Error(w, errors.Wrap(err, "Could not persist cookie").Error(), http.StatusBadRequest)
			return
		}

		// Redirect the user back to the consent endpoint. In a normal app, you would probably
		// add some logic here that is triggered when the user actually performs authentication and is not
		// part of the consent flow.
		http.Redirect(w, r, fmt.Sprintf("%s/consent?challenge=", consentURL)+challenge, http.StatusFound)
		return
	}

	// It's a get request, so let's render the template
	renderTemplate(w, "login.html", challenge)
}

// Once the user gave his consent, we will hit this endpoint. Again, this is not something that would
// be included in a traditional consent app, but we added it so you can see the data once the consent flow is done.
func handleCallback(w http.ResponseWriter, r *http.Request) {
	log.Printf("handleCallback")

	// in the real world you should check the state query parameter, but this is omitted for brevity reasons.

	// Exchange the access code for an access (and optionally) a refresh token
	token, err := client.OAuth2Config(fmt.Sprintf("%s/callback", consentURL)).Exchange(context.Background(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, errors.Wrap(err, "Could not exhange token").Error(), http.StatusBadRequest)
		return
	}

	// Render the output
	renderTemplate(w, "callback.html", struct {
		*oauth2.Token
		IDToken interface{}
	}{
		Token:   token,
		IDToken: token.Extra("id_token"),
	})
}

// authenticated checks if our cookie store has a user stored and returns the user's name, or an empty string if he is not authenticated.
func authenticated(r *http.Request) (user string) {
	session, _ := store.Get(r, sessionName)
	if u, ok := session.Values["user"]; !ok {
		return ""
	} else if user, ok = u.(string); !ok {
		return ""
	}
	return user
}

// renderTemplate is a convenience helper for rendering templates.
func renderTemplate(w http.ResponseWriter, id string, d interface{}) bool {
	if t, err := template.New(id).ParseFiles("./templates/" + id); err != nil {
		http.Error(w, errors.Wrap(err, "Could not render template").Error(), http.StatusInternalServerError)
		return false
	} else if err := t.Execute(w, d); err != nil {
		http.Error(w, errors.Wrap(err, "Could not render template").Error(), http.StatusInternalServerError)
		return false
	}
	return true
}
