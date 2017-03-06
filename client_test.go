package sse

// test scenarios:

//  "happy" case - connect to a server
//    * client reading just messages
//    * client watching errors
//    * client watching for "open"

//  "reconnect" cases -
//    * reconnects by default
//    * with a negotiated retry
//    * client watching for "closed"
