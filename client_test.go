package sse

// test scenarios:

//  "happy" case - connect to a server
//    * client reading just messages
//    * client watching errors
//    * client watching for "open"
//    * server sends keepalives

//  "reconnect" cases -
//    * reconnects by default
//    * with a negotiated retry
//    * with a last-event-id
//    * client watching for "closed"
