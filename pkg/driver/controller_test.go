package driver

import (
	"fmt"
	"testing"
)

func TestControllerServer_GetAttachVolumesAndReset(t *testing.T) {
	s := newControllerServer(nil)
	s.addAttachVolume("test", "node1")
	s.addAttachVolume("test", "node1")
	s.addAttachVolume("test", "node1")
	s.addAttachVolume("test2", "node1")
	fmt.Println(s.GetAttachVolumesAndReset("node1"))

	s.addDetachVolume("test", "node1")
	s.addDetachVolume("test", "node1")
	s.addDetachVolume("test", "node1")
	s.addDetachVolume("test2", "node1")
	fmt.Println(s.GetDetachVolumesAndReset("node1"))
}
