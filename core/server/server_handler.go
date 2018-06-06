package server

import (
	"errors"
	"regexp"
	"strings"
	"sync"

	"github.com/golang/glog"
	hubclient "github.com/sgoby/myhub/core/client"
	"github.com/sgoby/myhub/mysql"
	"github.com/sgoby/sqlparser"
	"github.com/sgoby/sqlparser/sqltypes"
	"time"
	"fmt"
	"strconv"
	"github.com/sgoby/myhub/core"
)

type ServerHandler struct {
	connectorMap map[uint32]*hubclient.Connector
	mu           *sync.Mutex
}

//
func NewServerHandler() *ServerHandler {
	mServerHandler := new(ServerHandler)
	mServerHandler.connectorMap = make(map[uint32]*hubclient.Connector)
	mServerHandler.mu = new(sync.Mutex)
	return mServerHandler
}

/*
NewConnection(c *Conn)

// ConnectionClosed is called when a connection is closed.
ConnectionClosed(c *Conn)

// ComQuery is called when a connection receives a query.
// Note the contents of the query slice may change after
// the first call to callback. So the Handler should not
// hang on to the byte slice.
ComQuery(conn interface{}, query string, callback func(*sqltypes.Result) error) error
*/

//NewConnection is implement of Handler interface on server.go
func (this *ServerHandler) NewConnection(c *mysql.Conn) interface{} {
	return this.addConnector(c)
}

//ConnectionClosed is implement of Handler interface on server.go
func (this *ServerHandler) ConnectionClosed(c *mysql.Conn) {
	this.delConnector(c)
}

//QueryTimeRecord is implement of Handler interface on server.go
func (this *ServerHandler) QueryTimeRecord(query string, startTime time.Time){
	slowTime := core.App().GetSlowLogTime()
	if slowTime <= 0{
		return
	}
	millisecond := float64(time.Now().Sub(startTime).Nanoseconds()) / float64(1000000)
	if millisecond < float64(slowTime){
		return
	}
	glog.Slow(fmt.Sprintf("%s [use: %.2f]",query,millisecond))
}

//ComQuery is implement of Handler interface on server.go
func (this *ServerHandler) ComQuery(conn interface{}, query string, callback func(*sqltypes.Result) error) error {
	mConnector,ok := conn.(*hubclient.Connector)
	if !ok{
		return errors.New("not connect!")
	}
	mConnector.UpActiveTime()
	//
	glog.Query("Query: ", query)
	if this.isBlacklistQuery(query){
		return fmt.Errorf("Myhub refused execute: %s",query)
	}
	//
	stmt, err := sqlparser.Parse(query)
	if err != nil {
		reg, err := regexp.Compile("^\\/\\*.+?\\*\\/$")
		if reg.MatchString(query) {
			callback(&sqltypes.Result{})
			return nil
		}
		//set names 'utf8' collate 'utf8_unicode_ci'
		reg, err = regexp.Compile("^set.*collate")
		if reg.MatchString(query) {
			callback(&sqltypes.Result{})
			return nil
		}
		//kill
		if rs,err, isVersion := this.comKill(query); isVersion || err != nil{
			if err != nil{
				return err
			}
			callback(rs)
			return nil
		}
		return err
	}
	//
	rs, err := mConnector.ComQuery(stmt, query)
	//
	defer glog.Flush()
	//
	if err != nil {
		return err
	}
	err = callback(&rs)
	if err != nil {
		return err
	}
	return nil
}

//if the query in the blacklist, Myhub will refuse execute
func (this *ServerHandler) isBlacklistQuery(query string) bool{
	return false
}

//NewConnection is implement of IServerHandler interface on conn.go
func (this *ServerHandler) GetConnectorMap() map[uint32]*hubclient.Connector{
	tMap := make(map[uint32]*hubclient.Connector)
	for key,val := range this.connectorMap{
		tMap[key] = val
	}
	return tMap;
}
//
func (this *ServerHandler) comKill(query string) (rs *sqltypes.Result,err error,ok bool) {
	query = strings.Replace(query,"`","",-1)
	query = strings.Replace(query,"\n","",-1)
	query = strings.ToLower(query)
	tokens := strings.Split(query," ")
	cmdKill := ""
	cmdKillIdStr := ""
	for _,token := range tokens{
		if len(cmdKill) <= 0 && token == "kill"{
			cmdKill = token
			continue
		}
		if len(cmdKill) > 0 && len(cmdKillIdStr) <= 0{
			cmdKillIdStr = token
			break;
		}
	}
	if len(cmdKill) <= 0{
		return nil,nil,false
	}
	//
	id,err := strconv.ParseInt(cmdKillIdStr,10,64)
	if err != nil{
		return nil,err,true;
	}
	//
	c := this.getConnectorById(id);
	if c == nil{
		return nil,fmt.Errorf("no connection of :%d",id),true;
	}
	err = c.Close()
	return &sqltypes.Result{RowsAffected:1},err,true;
}

//get total number of all connector
func (this *ServerHandler) getConnectorCount() int {
	return len(this.connectorMap)
}

//
func (this *ServerHandler) getConnector(c *mysql.Conn) *hubclient.Connector {
	this.mu.Lock()
	conn, ok := this.connectorMap[c.ConnectionID]
	this.mu.Unlock()
	if ok {
		return conn
	}
	return nil
}
//
func (this *ServerHandler) getConnectorById(id int64) *hubclient.Connector {
	this.mu.Lock()
	conn, ok := this.connectorMap[uint32(id)]
	this.mu.Unlock()
	if ok {
		return conn
	}
	return nil
}

//add a client connector when a new client connected
func (this *ServerHandler) addConnector(c *mysql.Conn) *hubclient.Connector {
	mConnector := hubclient.NewConnector(c)
	mConnector.SetServerHandler(this)
	//
	this.mu.Lock()
	conn, ok := this.connectorMap[c.ConnectionID]
	this.connectorMap[c.ConnectionID] = mConnector
	this.mu.Unlock()
	if ok && conn != nil {
		conn.Close()
	}
	//
	return mConnector
}

//delete a client connector when client closed.
func (this *ServerHandler) delConnector(c *mysql.Conn) {
	//
	this.mu.Lock()
	conn, ok := this.connectorMap[c.ConnectionID]
	delete(this.connectorMap, c.ConnectionID)
	this.mu.Unlock()
	if ok && conn != nil {
		conn.Close()
	}
}
