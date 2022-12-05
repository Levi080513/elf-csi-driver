// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

func (c *controllerServer) addVolumeToVolumesToBeAttached(volumeID, nodeName string) {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()

	_, ok := c.volumesToBeAttachedMap[nodeName]
	if !ok {
		c.volumesToBeAttachedMap[nodeName] = []string{}
	}

	for _, attachVolume := range c.volumesToBeAttachedMap[nodeName] {
		if volumeID == attachVolume {
			return
		}
	}

	c.volumesToBeAttachedMap[nodeName] = append(c.volumesToBeAttachedMap[nodeName], volumeID)
}

func (c *controllerServer) RemoveVolumeFromVolumesToBeAttached(volumeID, nodeName string) {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()

	_, ok := c.volumesToBeAttachedMap[nodeName]
	if !ok {
		return
	}

	volumesToBeAttached := []string{}

	for _, attachVolume := range c.volumesToBeAttachedMap[nodeName] {
		if volumeID == attachVolume {
			continue
		}

		volumesToBeAttached = append(volumesToBeAttached, attachVolume)
	}

	c.volumesToBeAttachedMap[nodeName] = volumesToBeAttached
}

func (c *controllerServer) GetVolumesToBeAttachedAndReset(nodeName string) []string {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()
	volumesToBeAttached := c.volumesToBeAttachedMap[nodeName]
	c.volumesToBeAttachedMap[nodeName] = []string{}

	return volumesToBeAttached
}

func (c *controllerServer) GetVolumesToBeDetachedAndReset(nodeName string) []string {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()
	volumesToBeDetached := c.volumesToBeDetachedMap[nodeName]
	c.volumesToBeDetachedMap[nodeName] = []string{}

	return volumesToBeDetached
}

func (c *controllerServer) addVolumeToVolumesToBeDetached(volumeID, nodeName string) {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()

	_, ok := c.volumesToBeDetachedMap[nodeName]
	if !ok {
		c.volumesToBeDetachedMap[nodeName] = []string{}
	}

	for _, detachVolume := range c.volumesToBeDetachedMap[nodeName] {
		if volumeID == detachVolume {
			return
		}
	}

	c.volumesToBeDetachedMap[nodeName] = append(c.volumesToBeDetachedMap[nodeName], volumeID)
}

func (c *controllerServer) RemoveVolumeFromVolumesToBeDetached(volumeID, nodeName string) {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()

	_, ok := c.volumesToBeDetachedMap[nodeName]
	if !ok {
		return
	}

	volumesToBeDetached := []string{}

	for _, detachVolume := range c.volumesToBeDetachedMap[nodeName] {
		if volumeID == detachVolume {
			continue
		}

		volumesToBeDetached = append(volumesToBeDetached, detachVolume)
	}

	c.volumesToBeDetachedMap[nodeName] = volumesToBeDetached
}
