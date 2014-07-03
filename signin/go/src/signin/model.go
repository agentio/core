package main

import (
	"labix.org/v2/mgo/bson"
	"time"
)

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

