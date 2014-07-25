//
//  Copyright 2014 Radtastical Inc.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.
//
package main

import (
	//"bytes"
	//"code.google.com/p/go-uuid/uuid"
	//"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"log"
	"net/http"
	"strings"
	"time"
)

type Node struct {
	Id         bson.ObjectId `bson:"_id,omitempty"`
	ParentId   bson.ObjectId
	Collection bool
	Name       string
	Hash       string
	Length     uint64
	Created    time.Time
	Modified   time.Time
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func getMongoSession() (mongoSession *mgo.Session) {
	mongoSession, err := mgo.Dial("127.0.0.1")
	if err != nil {
		panic(err)
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	return mongoSession
}

func rootNodeId() bson.ObjectId {
	return bson.ObjectIdHex("000000000000000000000000")
}

func getNodeForPath(path string, node *Node) (err error) {
	parts := strings.Split(path, "/")
	if parts[0] != "" {
		return errors.New("paths must begin with a /")
	}
	node.Id = rootNodeId()
	mongoSession := getMongoSession()
	for i, part := range parts {
		if i == 0 {
			continue
		}
		nodesCollection := mongoSession.DB("files").C("nodes")
		err = nodesCollection.Find(bson.M{"name": part, "parentid": node.Id}).One(&node)
		if err != nil {
			return err
		}
	}
	return nil
}

func getParentIdForPath(path string, parentId *bson.ObjectId) (err error) {
	fmt.Printf("path: %v\n", path)
	parts := strings.Split(path, "/")
	if parts[0] != "" {
		return errors.New("paths must begin with a /")
	}
	*parentId = rootNodeId()
	mongoSession := getMongoSession()
	nodesCollection := mongoSession.DB("files").C("nodes")
	for i, part := range parts {
		if i == 0 {
			continue
		}
		var node Node
		err = nodesCollection.Find(bson.M{"name": part, "parentid": *parentId}).One(&node)
		if err != nil {
			return err
		} else {
			*parentId = node.Id
		}
	}
	return nil
}

func parentPath(path string) string {
	parts := strings.Split(path, "/")
	return strings.Join(parts[0:len(parts)-1], "/")
}

func childName(path string) string {
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

func makeNodeForPath(path string, collection bool, node *Node) (err error) {
	err = getNodeForPath(path, node)
	if err == nil {
		fmt.Printf("found node %v\n", path)
		return nil
	}
	var parentId bson.ObjectId
	err = getParentIdForPath(parentPath(path), &parentId)
	if err == nil {
		mongoSession := getMongoSession()
		nodesCollection := mongoSession.DB("files").C("nodes")
		var newNode Node
		newNode.Id = bson.NewObjectId()
		newNode.ParentId = parentId
		newNode.Name = childName(path)
		newNode.Collection = collection
		nodesCollection.Insert(&newNode)
		return nil
	} else {
		return err
	}
}

func getRequestHandler(w http.ResponseWriter, r *http.Request) {
	// If the node doesn't exist, report an error
	path := r.URL.Path
	var node Node
	err := getNodeForPath(path, &node)
	if err != nil {
		http.Error(w, "Not found", 404)
		return
	}
	if node.Collection {
		http.Error(w, "GET for collections is unsupported", 400)
		return
	} else {
		hash := node.Hash
		mongoSession := getMongoSession()
		db := mongoSession.DB("files")
		file, err := db.GridFS("blobs").Open(hash)
		check(err)
		data, err := ioutil.ReadAll(file)
		w.Write(data)
	}
}

func getChildrenForNode(node Node, children *[]Node) (err error) {
	mongoSession := getMongoSession()
	nodesCollection := mongoSession.DB("files").C("nodes")
	err = nodesCollection.Find(bson.M{"parentid": node.Id}).All(children)
	return err
}

func deleteNode(node *Node) (err error) {
	if node.Collection {
		var children []Node
		err = getChildrenForNode(*node, &children)
		if err == nil {
			for _, child := range children {
				deleteNode(&child)
			}
		}
	}
	mongoSession := getMongoSession()
	nodesCollection := mongoSession.DB("files").C("nodes")
	err = nodesCollection.Remove(bson.M{"_id": node.Id})
	return err
}

func deleteRequestHandler(w http.ResponseWriter, r *http.Request) {
	var err error
	// If the request path has a fragment, report an error
	if len(r.URL.Fragment) > 0 {
		http.Error(w, "DELETE path has nonempty fragment", 409)
		return
	}
	// If the node doesn't exist, report an error
	path := strings.TrimRight(r.URL.Path, "/")
	var node Node
	err = getNodeForPath(path, &node)
	if err != nil {
		http.Error(w, "DELETE node does not exist", 404)
		return
	}
	// Delete the node
	err = deleteNode(&node)
	if err != nil {
		http.Error(w, "DELETE server error", 500)
	} else {
		w.WriteHeader(200)
		w.Write([]byte("DELETE deleted"))
	}
}

func putRequestHandler(w http.ResponseWriter, r *http.Request) {
	// load and hash the put data
	appfiledata, err := ioutil.ReadAll(r.Body)

	fmt.Printf("%v", string(appfiledata))

	check(err)
	hasher := sha1.New()
	hasher.Write(appfiledata)
	hash := hex.EncodeToString(hasher.Sum(nil))
	// get the node that we'll associate with the data
	path := r.URL.Path
	var node Node
	err = getNodeForPath(path, &node)
	if err == nil {
		// the node exists
		if node.Collection {
			http.Error(w, "PUT can't put a file onto a directory", 409)
			return
		} else {
			mongoSession := getMongoSession()
			nodesCollection := mongoSession.DB("files").C("nodes")
			update := map[string]interface{}{
				"hash":   hash,
				"length": len(appfiledata),
			}
			nodesCollection.Update(bson.M{"_id": node.Id}, bson.M{"$set": update})
			w.WriteHeader(200)
		}
	} else {
		// the node doesn't exist
		// If the parent directory doesn't exist, report an error
		var parentId bson.ObjectId
		err = getParentIdForPath(parentPath(path), &parentId)
		if err != nil {
			http.Error(w, "MKCOL no parent directory", 409)
			return
		}
		mongoSession := getMongoSession()
		nodesCollection := mongoSession.DB("files").C("nodes")
		var newNode Node
		newNode.Id = bson.NewObjectId()
		newNode.ParentId = parentId
		newNode.Name = childName(path)
		newNode.Collection = false
		newNode.Hash = hash
		newNode.Length = uint64(len(appfiledata))
		nodesCollection.Insert(&newNode)
		w.WriteHeader(201)
	}
	// store the put data
	mongoSession := getMongoSession()
	db := mongoSession.DB("files")
	file, err := db.GridFS("blobs").Create(hash)
	check(err)
	n, err := file.Write(appfiledata)
	check(err)
	err = file.Close()
	check(err)
	fmt.Printf("%d bytes written\n", n)
}

func mkcolRequestHandler(w http.ResponseWriter, r *http.Request) {
	// If the request body is not empty, report an error
	body, err := ioutil.ReadAll(r.Body)
	check(err)
	if len(body) > 0 {
		http.Error(w, "MKCOL request body must be empty", 415)
		return
	}
	// If a node with name already exists, report an error
	path := strings.TrimRight(r.URL.Path, "/")
	var node Node
	err = getNodeForPath(path, &node)
	if err == nil {
		fmt.Println("already exists")
		http.Error(w, "MKCOL file or collection already exists", 405)
		return
	}
	// If the parent directory doesn't exist, report an error
	var parentId bson.ObjectId
	err = getParentIdForPath(parentPath(path), &parentId)
	if err != nil {
		fmt.Println("no parent directory")

		http.Error(w, "MKCOL no parent directory", 409)
		return
	}
	// Make a node for the collection
	err = makeNodeForPath(path, true, &node)
	if err != nil {
		http.Error(w, "MKCOL server error", 500)
	} else {
		w.WriteHeader(201)
		w.Write([]byte("MKCOL created"))
	}
}

/*
(def copy-node (mongo sourceNode destinationParent destinationName)
     (set sourceID (sourceNode _id:))
     (sourceNode removeObjectForKey:"_id")
     (sourceNode parent:(destinationParent _id:))
     (if destinationName
         (sourceNode name:destinationName))
     (set destinationID (mongo insertObject:sourceNode
                             intoCollection:"files.nodes"))
     (sourceNode _id:destinationID)
     (if (sourceNode collection:)
         (set children (mongo findArray:(dict parent:sourceID) inCollection:"files.nodes"))
         (children each:
                   (do (child)
                       (copy-node mongo child sourceNode nil)))))

(copy "/*path:"
      (puts "COPY #{*path}")
      (set mongo (AgentMongoDB new))
      (mongo connect)

      (set pathToCopyFrom *path)
      (puts "PATH TO COPY FROM: #{pathToCopyFrom}")

      (set overwrite ((REQUEST headers) Overwrite:))
      (set destination (decode ((REQUEST headers) Destination:)))
      (puts "DESTINATION: #{destination}")

      (set destination (destination stringByReplacingOccurrencesOfString:(+ (server REQUEST) "/") withString:""))
      (if (eq (destination characterAtIndex:(- (destination length) 1)) '/')
          (set destination (destination substringWithRange:(list 0 (- (destination length) 1)))))
      (set pathToCopyTo destination)
      (puts "PATH TO COPY TO: #{pathToCopyTo}")

      (set sourceNode (node-for-path mongo pathToCopyFrom))
      (set destinationNode (node-for-path mongo pathToCopyTo))
      (set destinationParent (node-for-path mongo (pathToCopyTo stringByDeletingLastPathComponent)))

      (puts "copying #{pathToCopyFrom} to #{pathToCopyTo}")
      (puts "destination: #{pathToCopyTo} #{(destinationNode description)}")

      (cond ((and (eq overwrite "F") destinationNode)
             (puts "WE WILL NOT OVERRIDE #{pathToCopyTo}")
             (RESPONSE setStatus:412)
             "")
            ((not destinationParent)
             (set message "no destination directory: #{(pathToCopyTo stringByDeletingLastPathComponent)}")
             (RESPONSE setStatus:409)
             message)
            (else
                 (if destinationNode
                     (then (delete-node mongo destinationNode)
                           (RESPONSE setStatus:204))
                     (else (RESPONSE setStatus:201)))
                 (copy-node mongo sourceNode destinationParent (pathToCopyTo lastPathComponent))
                 "OK")))

*/

func copyRequestHandler(w http.ResponseWriter, r *http.Request) {
	sourcePath := strings.TrimRight(r.URL.Path, "/")

	overwrite := r.Header["Overwrite"]
	destination := r.Header["Destination"]

	fmt.Printf("source path %v\n", sourcePath)
	fmt.Printf("overwrite %v\n", overwrite)
	fmt.Printf("destination %v\n", destination)
	fmt.Printf("host %v\n", r.Host)

	parts := strings.Split(destination[0], r.Host)
	fmt.Printf("parts %+v\n", parts)
	
	destinationPath := strings.TrimRight(parts[1], "/")
	
	fmt.Printf("destination path %+v\n", destinationPath)

}

/*

(def move-node (mongo sourceNode destinationParent destinationName)
     (sourceNode parent:(destinationParent _id:))
     (if destinationName
         (sourceNode name:destinationName))
     (mongo updateObject:sourceNode
            inCollection:"files.nodes"
           withCondition:(dict _id:(sourceNode _id:))
       insertIfNecessary:YES
   updateMultipleEntries:NO))

(move "/*path:"
      (puts "MOVE #{*path}")
      (set mongo (AgentMongoDB new))
      (mongo connect)

      (set pathToMoveFrom *path)

      (set overwrite ((REQUEST headers) Overwrite:))

      (set destination (decode ((REQUEST headers) Destination:)))
      (puts "DESTINATION: #{destination}")

      (set destination (destination stringByReplacingOccurrencesOfString:(+ (server REQUEST) "/") withString:""))
      (if (eq (destination characterAtIndex:(- (destination length) 1)) '/')
          (set destination (destination substringWithRange:(list 0 (- (destination length) 1)))))

      (set pathToMoveTo destination)

      (set sourceNode (node-for-path mongo pathToMoveFrom))
      (set destinationNode (node-for-path mongo pathToMoveTo))
      (set destinationParent (node-for-path mongo (pathToMoveTo stringByDeletingLastPathComponent)))

      (puts "moving #{pathToMoveFrom} to #{pathToMoveTo}")
      (puts "destination exists? #{pathToMoveTo} #{destinationNode}")

      (cond ((and (eq overwrite "F") destinationNode)
             (RESPONSE setStatus:412)
             "")
            ((not destinationParent)
             (puts "no destination directory: #{(pathToMoveTo stringByDeletingLastPathComponent)}")
             (RESPONSE setStatus:409)
             "")
            (else
                 (if destinationNode
                     (delete-node mongo destinationNode))
                 (move-node mongo sourceNode destinationParent (pathToMoveTo lastPathComponent))
                 (if destinationNode
                     (then (RESPONSE setStatus:204))
                     (else (RESPONSE setStatus:201)))
                 "OK")))
*/

func moveRequestHandler(w http.ResponseWriter, r *http.Request) {
}

func propfindRequestHandler(w http.ResponseWriter, r *http.Request) {
}

func optionsRequestHandler(w http.ResponseWriter, r *http.Request) {
	w.Header()["Allow"] = []string{"OPTIONS, GET, HEAD, POST, PUT, DELETE, TRACE, COPY, MOVE, MKCOL, PROPFIND, PROPPATCH, LOCK, UNLOCK, ORDERPATCH"}
	w.Header()["DAV"] = []string{"1, 2"}
	w.WriteHeader(200)
	w.Write([]byte("OK"))
}

func lockRequestHandler(w http.ResponseWriter, r *http.Request) {
}

func unlockRequestHandler(w http.ResponseWriter, r *http.Request) {
}

func WebDAVRequestHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		getRequestHandler(w, r)
	case "DELETE":
		deleteRequestHandler(w, r)
	case "PUT":
		putRequestHandler(w, r)
	case "MKCOL":
		mkcolRequestHandler(w, r)
	case "COPY":
		copyRequestHandler(w, r)
	case "MOVE":
		moveRequestHandler(w, r)
	case "PROPFIND":
		propfindRequestHandler(w, r)
	case "OPTIONS":
		optionsRequestHandler(w, r)
	case "LOCK":
		lockRequestHandler(w, r)
	case "UNLOCK":
		unlockRequestHandler(w, r)
	}
}

var port = flag.Uint("p", 8080, "the port to use for serving HTTP requests")

func main() {
	flag.Parse()
	http.HandleFunc("/", WebDAVRequestHandler)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", *port), nil))
}
