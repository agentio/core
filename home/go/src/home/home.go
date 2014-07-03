package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"log"
	"net/http"
	"time"
)

func dumpRequest(request *http.Request) {
	fmt.Println("Form")
	for key, value := range request.Form {
		fmt.Println("Key:", key, "Value:", value)
	}
	fmt.Println("Headers")
	for key, value := range request.Header {
		fmt.Println("Key:", key, "Value:", value)
	}
}

func md5HashWithSalt(input, salt string) string {
	hasher := hmac.New(md5.New, []byte(salt))
	hasher.Write([]byte(input))
	return hex.EncodeToString(hasher.Sum(nil))
}

type Signin struct {
	Message  string
	Username string
	Password string
}

type User struct {
	ID       bson.ObjectId `bson:"_id,omitempty"`
	Username string
	Password string
}

type Session struct {
	ID       bson.ObjectId `bson:"_id,omitempty"`
	Value    string
	Username string
	Expires  time.Time
}

func (self Session) String() string {
	var buffer bytes.Buffer
	buffer.WriteString(self.Value)
	buffer.WriteString("; ")
	buffer.WriteString("Expires:")
	buffer.WriteString(self.Expires.Format(time.RFC1123))
	buffer.WriteString("; ")
	return buffer.String()
}

func getMongoSession() (mongoSession *mgo.Session) {
	mongoSession, err := mgo.Dial("127.0.0.1")
	if err != nil {
		panic(err)
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	return mongoSession
}

func getSessionAndUser(r *http.Request, mongoSession *mgo.Session) (session Session, user User, err error) {
	cookie, err := r.Cookie("session")
	if err != nil {
		user.Username = ""
		session.Username = ""
	} else {
		if err != nil {
			panic(err)
		}
		sessionsCollection := mongoSession.DB("accounts").C("sessions")
		err = sessionsCollection.Find(bson.M{"value": cookie.Value}).One(&session)
		username := session.Username
		usersCollection := mongoSession.DB("accounts").C("users")
		err = usersCollection.Find(bson.M{"username": username}).One(&user)
	}
	return session, user, err
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	session, user, err := getSessionAndUser(r, mongoSession)
	if err != nil {
		http.Redirect(w, r, "/signin", http.StatusFound)
	} else {
		fmt.Println(user.Username)
		t, _ := template.ParseFiles("templates/layout.html", "templates/home.html", "templates/topbar.html")
		title, err := template.New("title").Parse("Home")
		if err != nil {
			fmt.Println("There was an error", err)
		}
		x, _ := t.AddParseTree("title", title.Tree)
		err2 := x.ExecuteTemplate(w, "layout", session)
		if err2 != nil {
			fmt.Println("There was an error", err2)
		}
	}
}

func myNotFoundHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "Hello!")
}


var port = flag.Uint("p", 8080, "the port to use for serving HTTP requests")

func main() {
        http.HandleFunc("/", rootHandler)
	http.HandleFunc("/home", homeHandler)
	http.HandleFunc("/home/", homeHandler)

	flag.Parse()
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", *port), nil))
}
