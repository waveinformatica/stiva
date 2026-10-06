package auth

import (
	"strings"
	"testing"
)

const casSuccessDoc = `<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationSuccess>
    <cas:user>alice</cas:user>
    <cas:attributes>
      <cas:email>alice@example.com</cas:email>
      <cas:memberOf>devs</cas:memberOf>
      <cas:memberOf>admins</cas:memberOf>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`

// Same shape with a different namespace prefix and no attributes.
const casBareDoc = `<srv:serviceResponse xmlns:srv='http://www.yale.edu/tp/cas'>
<srv:authenticationSuccess><srv:user>bob</srv:user></srv:authenticationSuccess>
</srv:serviceResponse>`

const casFailureDoc = `<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationFailure code='INVALID_TICKET'>ticket not recognized</cas:authenticationFailure>
</cas:serviceResponse>`

func TestParseCASResponse(t *testing.T) {
	user, attrs, err := parseCASResponse([]byte(casSuccessDoc))
	if err != nil {
		t.Fatalf("success: %v", err)
	}
	if user != "alice" {
		t.Fatalf("user = %q", user)
	}
	if len(attrs["memberOf"]) != 2 || attrs["memberOf"][0] != "devs" || attrs["email"][0] != "alice@example.com" {
		t.Fatalf("attrs = %v", attrs)
	}

	user, attrs, err = parseCASResponse([]byte(casBareDoc))
	if err != nil || user != "bob" {
		t.Fatalf("bare success: user=%q err=%v", user, err)
	}
	if len(attrs) != 0 {
		t.Fatalf("bare success should carry no attrs: %v", attrs)
	}

	if _, _, err := parseCASResponse([]byte(casFailureDoc)); err == nil {
		t.Fatal("failure should error")
	} else if !strings.Contains(err.Error(), "INVALID_TICKET") {
		t.Fatalf("failure should name the code: %v", err)
	}

	for _, bad := range []string{"", "not xml", "<cas:serviceResponse/>"} {
		if _, _, err := parseCASResponse([]byte(bad)); err == nil {
			t.Fatalf("input %q should error", bad)
		}
	}
}
