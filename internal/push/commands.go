// Package push is not built yet. Its owner replaces this file.
//
// It owes the kit two things and binds no command. Push is one sealed
// message for each paired phone that handed over a contract.PushTo, sent to
// that phone maker's push service and to no other address, with the
// standard library alone; it answers how many it reached. The phones are
// read with contract.ReadPhones; a phone the service says is gone loses its
// Push through contract.ChangePhones, the one writer. PushKey is the public
// half of the login's key for nudges, push.key in the settings folder, made
// at first use and private to the login. What a phone hands over is stored
// by the page's server, which is where it arrives (contract.RoutePush). It
// does not decide whether a nudge is due: Mute, Do not disturb and
// nudge.phone are the notifier's, which calls Push last. With no phone that
// asked it sends nothing and is no error.
package push

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
