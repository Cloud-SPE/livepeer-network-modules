// Package e2e exercises the pool as a whole: controller, broker and a
// member's host, as separate processes talking over the wire.
//
// Every module here is tested on its own and each was right on its own.
// The defects that reached production shape were all in the SEAMS — a
// field the broker rejected that the controller sent, environment names
// the bundle wrote and the agent did not read, a retirement one package
// honoured and another ignored, an offer axis authored and silently
// dropped on the way across. Nothing in the repo ran the chain, so
// nothing could have caught them.
//
// This is deliberately BLACK BOX. It boots the real binaries and speaks
// HTTP and the attach tunnel exactly as a member's agent would, rather
// than importing the packages and calling in. Importing would test that
// the types line up; the failures worth catching are the ones where the
// types line up and the wire does not.
package e2e
