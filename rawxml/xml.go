package databath

import (
	"fmt"
	"github.com/daemonl/databath"
	"github.com/daemonl/databath/types"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
)

type xmlModel struct {
	Collections      []xmlCollection      `xml:"collection"`
	CustomQueries    []xmlCustomQuery     `xml:"query"`
	DynamicFunctions []xmlDynamicFunction `xml:"script"`
	Hooks            []xmlHook            `xml:"hook"`
}

type xmlCollection struct {
	Name           string            `xml:"name,attr"`
	Fields         xmlFields         `xml:"field"`
	FieldSets      []xmlFieldSet     `xml:"fieldset"`
	CustomFields   []xmlCustomField  `xml:"custom"`
	SearchPrefixes []xmlSearchPrefix `xml:"search-prefix"`
	//Masks          map[string]map[string][]interface{} `json:"masks"`
	ViewQuery *string `xml:"view-query,attr,omitempty"`
}

type xmlFields struct {
	Fields []xmlField `xml:",any"`
}

type xmlField struct {
	XMLName xml.Name `xml:"name"`
	// attributes...
}

type xmlFieldSet struct {
	Fields []xmlFieldSetFieldDef `xml:",any"`
}

type xmlFieldSetFieldDef struct {
	XMLName xml.Name `xml:"name"`
	Label   string   `xml:"label,omitempty"`
	Hidden  bool     `xml:"hidden,omitempty"`
}

type xmlCustomQuery struct {
	Query string `xml:"sql"`
	//InFields  []map[string]interface{}          `json:"parameters"`
	//OutFields map[string]map[string]interface{} `json:"columns"`
	Type string `xml:"type,attr"`
}
type xmlSearchPrefix struct {
	Field string `xml:"field"`
}

func ReadModelFromReader(modelReader io.ReadCloser, doFieldSets bool) (*Model, error) {
	log.Println("=== Model Init ===")

	var model rawModel
	decoder := json.NewDecoder(modelReader)
	err := decoder.Decode(&model)
	if err != nil {
		return nil, err
	}

	dynamicFunctions := model.DynamicFunctions

	customQueries := make(map[string]*CustomQuery)
	for queryName, rawQuery := range model.CustomQueries {
		//log.Printf("Custom Query: %s", queryName)
		cq := CustomQuery{
			Query:     rawQuery.Query,
			InFields:  make([]*Field, len(rawQuery.InFields), len(rawQuery.InFields)),
			OutFields: make(map[string]*Field),
			Type:      rawQuery.Type,
		}
		for i, rawField := range rawQuery.InFields {
			field, err := FieldFromDef(rawField)
			if err != nil {
				return nil, (fmt.Errorf("Error parsing Raw Query %s.[in][%d] - %s", queryName, i, err.Error()))
			}
			cq.InFields[i] = field
		}
		for i, rawField := range rawQuery.OutFields {
			field, err := FieldFromDef(rawField)
			if err != nil {
				return nil, (fmt.Errorf("Error parsing Raw Query %s.[out][%d] - %s", queryName, i, err.Error()))
			}
			cq.OutFields[i] = field
		}
		customQueries[queryName] = &cq
	}

	collections := make(map[string]*Collection)

	for collectionName, rawCollection := range model.Collections {
		//log.Printf("Read Collection %s\n", collectionName)
		fields := make(map[string]*Field)

		_, ok := rawCollection.Fields["id"]
		if !ok {
			return nil, (fmt.Errorf("Error parsing collection %s, no id field", collectionName))
		}

		for fieldName, rawField := range rawCollection.Fields {

			field, err := FieldFromDef(rawField)

			if err != nil {
				return nil, (fmt.Errorf("Error parsing %s.%s - %s", collectionName, fieldName, err.Error()))
			}
			field.Path = fieldName
			fields[fieldName] = field
		}

		customFields := make(map[string]FieldSetFieldDef)

		for name, rawCustomField := range rawCollection.CustomFields {
			fsfd, err := getFieldSetFieldDef(name, rawCustomField)
			if err != nil {
				err = fmt.Errorf("in collection %s: %s", collectionName, err.Error())
				log.Printf(err.Error())
				return nil, err
			}
			customFields[name] = fsfd
		}

		fieldSets := make(map[string][]FieldSetFieldDef)
		if doFieldSets {

			if rawCollection.FieldSets == nil {
				rawCollection.FieldSets = make(map[string][]string)
			}

			_, hasDefaultFieldset := rawCollection.FieldSets["default"]
			if !hasDefaultFieldset {
				allFieldNames := make([]string, 0, 0)
				for fieldName, _ := range rawCollection.Fields {
					allFieldNames = append(allFieldNames, fieldName)
				}
				rawCollection.FieldSets["default"] = allFieldNames

			}

			_, hasIdentityFieldset := rawCollection.FieldSets["identity"]
			if !hasIdentityFieldset {
				_, exists := rawCollection.Fields["name"]
				if !exists {
					return nil, (fmt.Errorf("%s: No identity fieldset or 'name' field to fall back on.", collectionName))
				}

				rawCollection.FieldSets["identity"] = []string{"name"}
			}

			for name, rawSet := range rawCollection.FieldSets {
				//log.Printf("Evaluate Fieldset: %s", name)
				rawSet = append(rawSet, "id")

				fieldSetDefs := make([]FieldSetFieldDef, len(rawSet), len(rawSet))
				for i, fieldName := range rawSet {
					if fieldName[0:1] == "-" {
						fieldName = fieldName[1:]
					}

					fieldName = strings.Split(fieldName, " ")[0]

					customField, ok := customFields[fieldName]
					if ok {
						fieldSetDefs[i] = customField
						continue
					}

					fsfd := FieldSetFieldDefNormal{
						path:      fieldName,
						pathSplit: strings.Split(fieldName, "."),
					}
					fieldSetDefs[i] = &fsfd

					//return nil, UserErrorF("No field or custom field for %s in %s", fieldName, collectionName)

				}
				fieldSets[name] = fieldSetDefs
			}
		}

		searchPrefixes := make(map[string]*SearchPrefix)
		for prefixStr, rawPrefix := range rawCollection.SearchPrefixes {
			//field, ok := fields[rawPrefix.Field]
			//if !ok {
			//	return nil, ParseErrF("Prefix referenced field '%s' which doesn't exist", rawPrefix.Field)
			//}
			prefix := SearchPrefix{
				//Field:     field,
				Prefix:    prefixStr,
				FieldName: rawPrefix.Field,
			}
			searchPrefixes[prefixStr] = &prefix
		}

		masks := map[uint64]*Mask{}

		for users, rawMask := range rawCollection.Masks {

			r, rok := rawMask["read"]
			w, wok := rawMask["write"]

			mask := &Mask{}
			if rok {
				mask.Read = make([]string, len(r), len(r))
				for i, name := range r {
					str, ok := name.(string)
					if !ok {
						return nil, ParseErrF("Mask fieldset name not string")
					}
					mask.Read[i] = str
				}
			}
			if wok {
				mask.Write = make([]string, len(r), len(r))
				for i, name := range w {
					str, ok := name.(string)
					if !ok {
						return nil, ParseErrF("Mask fieldset name not string")
					}
					mask.Write[i] = str
				}
			}

			for _, uPart := range strings.Split(users, ",") {
				subUParts := strings.Split(uPart, "-")
				switch len(subUParts) {
				case 1:
					asInt, err := strconv.ParseUint(subUParts[0], 10, 64)
					if err != nil {
						return nil, ParseErrF("Mask identifier invalid %s", uPart)
					}
					masks[asInt] = mask

				case 2:
					asIntFrom, err1 := strconv.ParseUint(subUParts[0], 10, 64)
					asIntTo, err2 := strconv.ParseUint(subUParts[0], 10, 64)
					if err1 != nil || err2 != nil || asIntFrom > asIntTo {
						return nil, ParseErrF("Mask identifier invalid %s", uPart)
					}

					for i := asIntFrom; i <= asIntTo; i++ {
						masks[i] = mask
					}

				}
			}
		}

		collection := Collection{
			Fields:         fields,
			FieldSets:      fieldSets,
			TableName:      collectionName,
			ForeignKeys:    make([]*Field, 0, 0),
			CustomFields:   customFields,
			SearchPrefixes: searchPrefixes,
			Masks:          masks,
			ViewQuery:      rawCollection.ViewQuery,
		}

		collections[collectionName] = &collection
	}

	for _, h := range model.Hooks {

		if h.Raw != nil {
			rawQuery := h.Raw
			//log.Println("Custom Query in Hook")
			cq := CustomQuery{
				Query:     rawQuery.Query,
				InFields:  make([]*Field, len(rawQuery.InFields), len(rawQuery.InFields)),
				OutFields: make(map[string]*Field),
				Type:      rawQuery.Type,
			}
			for i, rawField := range rawQuery.InFields {
				field, err := FieldFromDef(rawField)
				if err != nil {
					log.Println(err)
					return nil, (fmt.Errorf("Error parsing hook ", err.Error()))
				}
				cq.InFields[i] = field
			}
			//log.Println("DONE")
			h.CustomAction = &cq
		}

		collection, ok := collections[h.Collection]
		if !ok {
			return nil, UserErrorF("Hook on non existing collection %s", h.Collection)
		}
		collection.Hooks = append(collection.Hooks, h)

	}

	returnModel := &Model{
		Collections:      collections,
		CustomQueries:    customQueries,
		DynamicFunctions: dynamicFunctions,
	}

	for _, collection := range collections {
		collection.Model = returnModel
		for path, field := range collection.Fields {
			field.Collection = collection
			field.Path = path

			refField, isRefField := field.Impl.(*types.FieldRef)
			if !isRefField {
				continue
			}
			_, ok := collections[refField.Collection]
			if !ok {
				return nil, UserErrorF("ref field %s.%s references collection %s, which doesn't exist", collection.TableName, path, refField.Collection)
			}
			collections[refField.Collection].ForeignKeys = append(collections[refField.Collection].ForeignKeys, field)
		}

		// Check all fieldsets...

	}

	log.Println("=== End Model Init ===")
	return returnModel, err
}

func ReadModelFromFileForSync(filename string) (*Model, error) {

	modelFile, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	m, err := ReadModelFromReader(modelFile, false)
	modelFile.Close()
	return m, err
}
func ReadModelFromFile(filename string) (*Model, error) {
	modelFile, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	m, err := ReadModelFromReader(modelFile, true)
	modelFile.Close()
	return m, err
}

func getFieldParamString(rawField map[string]interface{}, paramName string) (*string, error) {
	val, ok := rawField[paramName]
	if !ok {
		return nil, nil
	}
	str, ok := val.(string)
	if !ok {
		return nil, (fmt.Errorf("param %s value must be a string", paramName))
	}
	return &str, nil
}

func getFieldParamInt(rawField map[string]interface{}, paramName string) (*int64, error) {
	val, ok := rawField[paramName]
	if !ok {
		return nil, nil
	}
	intval, ok := val.(int64)
	if !ok {
		return nil, (fmt.Errorf("param %s value must be an integer", paramName))
	}
	return &intval, nil
}
