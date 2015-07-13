package datastream

import (
	"database/sql"
	"errors"
)

/*
The datastream table:

CREATE TABLE IF NOT EXISTS datastream (
    StreamId BIGINT NOT NULL,
	Substream VARCHAR,
    EndTime DOUBLE PRECISION,
    EndIndex BIGINT,
	Version INTEGER,
    Data BYTEA,
    UNIQUE (StreamId, Substream, EndIndex),
    PRIMARY KEY (StreamId, Substream, EndIndex)
    );
*/

var (
	//ErrorDatabaseCorrupted is returned when there is data loss or inconsistency in the database
	ErrorDatabaseCorrupted = errors.New("Database is corrupted!")
	//ErrWTF is returned when an internal assertion fails - it shoudl not happen. Ever.
	ErrWTF = errors.New("Something is seriously wrong. A internal assertion failed.")
)

//The SqlStore stores and queries arrays of Datapoints in an SQL database. The table 'datastream' is assumed
//to already exist and the correct indices are assumed to already exist.
type SqlStore struct {
	inserter     *sql.Stmt
	timequery    *sql.Stmt
	indexquery   *sql.Stmt
	endindex     *sql.Stmt
	delsubstream *sql.Stmt
	delstream    *sql.Stmt
	clearall     *sql.Stmt

	insertversion int
}

//This function is to allow daisy-chaining errors from statement creation
func prepStatement(db *sql.DB, statement string, err error) (*sql.Stmt, error) {
	if err != nil {
		return nil, err
	}
	return db.Prepare(statement)
}

//prepareSqlStore sets up the inserts (it assumes that the database was already prepared)
func prepareSqlStore(db *sql.DB, insertStatement, timequeryStatement, indexqueryStatement,
	endindexStatement, delsubstreamStatement, delstreamStatement, clearallStatement string) (*SqlStore, error) {
	if err := db.Ping(); err != nil {
		return nil, err
	}

	inserter, err := prepStatement(db, insertStatement, nil)
	timequery, err := prepStatement(db, timequeryStatement, err)
	indexquery, err := prepStatement(db, indexqueryStatement, err)
	endindex, err := prepStatement(db, endindexStatement, err)
	delsubstream, err := prepStatement(db, delsubstreamStatement, err)
	delstream, err := prepStatement(db, delstreamStatement, err)
	clearall, err := prepStatement(db, clearallStatement, err)

	ss := &SqlStore{inserter, timequery, indexquery, endindex, delsubstream, delstream, clearall, 2}

	if err != nil {
		ss.Close()
		return nil, err
	}

	return ss, nil
}

//OpenPostgresStore initializes a postgres database to work with an SqlStore.
func OpenPostgresStore(db *sql.DB) (*SqlStore, error) {
	return prepareSqlStore(db, "INSERT INTO datastream VALUES ($1,$2,$3,$4,$5,$6);",
		"SELECT Version,EndIndex,Data FROM datastream WHERE StreamID=$1 AND Substream=$2 AND EndTime > $3 ORDER BY EndTime ASC;",
		"SELECT Version,EndIndex,Data FROM datastream WHERE StreamID=$1 AND Substream=$2 AND EndIndex > $3 ORDER BY EndIndex ASC;",
		"SELECT COALESCE(MAX(EndIndex),0) FROM datastream WHERE StreamID=$1 AND Substream=$2;",
		"DELETE FROM datastream WHERE StreamID=$1 AND Substream=$2;",
		"DELETE FROM datastream WHERE StreamID=$1;",
		"DELETE FROM datastream;")
}

//OpenSqlStore uses the correct initializer for the given database driver
func OpenSqlStore(db *sql.DB) (*SqlStore, error) {
	return OpenPostgresStore(db)
}

//Close all resources associated with the SqlStore.
func (s *SqlStore) Close() {
	//The if statements allow to close a partially initialized store
	if s.inserter != nil {
		s.inserter.Close()
	}
	if s.timequery != nil {
		s.timequery.Close()
	}
	if s.indexquery != nil {
		s.indexquery.Close()
	}
	if s.endindex != nil {
		s.endindex.Close()
	}
	if s.delstream != nil {
		s.delstream.Close()
	}
	if s.delsubstream != nil {
		s.delsubstream.Close()
	}
}

//Clear the entire table of all data
func (s *SqlStore) Clear() error {
	_, err := s.clearall.Exec()
	return err
}

//GetEndIndex returns the first index point outside of the most recent datapointarray stored within the database.
//In effect, if the datapoints in a key were all in one huge array, returns array.length
//(not including the datapoints which are not yet committed to the SqlStore)
func (s *SqlStore) GetEndIndex(streamID int64, substream string) (ei int64, err error) {
	rows, err := s.endindex.Query(streamID, substream)
	if err != nil {
		return 0, err
	}
	if !rows.Next() {
		return 0, ErrWTF //This should never happen
	}
	err = rows.Scan(&ei)
	rows.Close()
	return ei, err
}

//Insert the given DatapointArray into the sql database given the startindex of the array for the key.
func (s *SqlStore) Insert(streamID int64, substream string, startindex int64, da DatapointArray) error {
	dbytes, err := da.Encode(s.insertversion)
	if err != nil {
		return err
	}
	_, err = s.inserter.Exec(streamID, substream, da[len(da)-1].Timestamp, startindex+int64(len(da)),
		s.insertversion, dbytes)
	return err
}

//WriteBatches writes the given batch array
func (s *SqlStore) WriteBatches(b []Batch) error {
	for i := 0; i < len(b); i++ {
		streamID, err := b[i].GetStreamID()
		if err != nil {
			return err
		}
		err = s.Insert(streamID, b[i].Substream, b[i].StartIndex, b[i].Data)
		if err != nil {
			return err
		}
	}
	return nil
}

//Append the given DatapointArray to the data stream for key
func (s *SqlStore) Append(streamID int64, substream string, dp DatapointArray) error {
	i, err := s.GetEndIndex(streamID, substream)
	if err != nil {
		return err
	}
	return s.Insert(streamID, substream, i, dp)
}

//DeleteStream deletes all data associated with the given stream in the database
func (s *SqlStore) DeleteStream(streamID int64) error {
	_, err := s.delstream.Exec(streamID)
	return err
}

//DeleteSubstream deletes all data associated with the given substream in the database
func (s *SqlStore) DeleteSubstream(streamID int64, substream string) error {
	_, err := s.delsubstream.Exec(streamID, substream)
	return err
}

//GetByTime returns a DataRange of datapoints starting at the starttime
func (s *SqlStore) GetByTime(streamID int64, substream string, starttime float64) (dr DataRange, startindex int64, err error) {
	rows, err := s.timequery.Query(streamID, substream, starttime)
	if err != nil {
		return nil, 0, err
	}

	if !rows.Next() { //Check if there is any data to read
		startindex, err = s.GetEndIndex(streamID, substream)
		if rows.Err() != nil {
			err = rows.Err()
		}
		rows.Close()
		return EmptyRange{}, startindex, err
	}

	//There is some data!
	var version int
	var endindex int64
	var data []byte
	if err = rows.Scan(&version, &endindex, &data); err != nil {
		return EmptyRange{}, endindex, err
	}

	da, err := DecodeDatapointArray(data, version)
	if err != nil {
		rows.Close()
		return EmptyRange{}, endindex, err
	}
	tmp := da.TStart(starttime)
	da = &tmp
	if da == nil || int64(da.Length()) > endindex {
		rows.Close()
		return EmptyRange{}, endindex, ErrorDatabaseCorrupted
	}

	return &SqlRange{rows, da}, endindex - int64(da.Length()), nil
}

//GetByIndex returns a DataRange of datapoints starting at the nearest dataindex to the given startindex
func (s *SqlStore) GetByIndex(streamID int64, substream string, startindex int64) (dr DataRange, dataindex int64, err error) {
	rows, err := s.indexquery.Query(streamID, substream, startindex)
	if err != nil {
		return nil, 0, err
	}

	if !rows.Next() { //Check if there is any data to read
		startindex, err = s.GetEndIndex(streamID, substream)
		if rows.Err() != nil {
			err = rows.Err()
		}
		rows.Close()
		return EmptyRange{}, startindex, err
	}

	//There is some data!
	var version int
	var endindex int64
	var data []byte
	if err = rows.Scan(&version, &endindex, &data); err != nil {
		return EmptyRange{}, endindex, err
	}

	da, err := DecodeDatapointArray(data, version)
	if err != nil {
		rows.Close()
		return EmptyRange{}, endindex, err
	}

	if da == nil || int64(da.Length()) > endindex {
		rows.Close()
		return EmptyRange{}, endindex, ErrorDatabaseCorrupted
	}

	//Lastly, we start the DatapointArray from the correct index
	//This subtraction is guaranteed to work, since query requires $gt
	fromend := endindex - startindex
	if fromend < int64(da.Length()) {
		//The index we want is within the datarange
		da = da.IRange(da.Length()-int(fromend), da.Length())
	}

	return &SqlRange{rows, da}, endindex - int64(da.Length()), nil
}