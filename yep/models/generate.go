// Copyright 2016 NDP Systèmes. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package models

import (
	"fmt"
	"path"
	"reflect"
	"strings"
	"text/template"

	"github.com/npiganeau/yep/yep/tools/generate"
)

// A fieldData describes a field in a RecordSet
type fieldData struct {
	Name         string
	RelModel     string
	IsSearchable bool
	Type         string
	SanType      string
	TypeIsRS     bool
}

// A returnType characterizes a return value of a method
type returnType struct {
	Type string
	IsRS bool
}

// A methodData describes a method in a RecordSet
type methodData struct {
	Name           string
	Doc            string
	Params         string
	ParamsWithType string
	ReturnAsserts  string
	Returns        string
	ReturnString   string
	Call           string
}

// an operatorDef defines an operator func
type operatorDef struct {
	Name  string
	Multi bool
}

// An fieldType holds the name and valid operators on a field type
type fieldType struct {
	Type      string
	SanType   string
	TypeIsRS  bool
	Operators []operatorDef
}

// A modelData describes a RecordSet model
type modelData struct {
	Name           string
	Deps           []string
	Fields         []fieldData
	Methods        []methodData
	ConditionFuncs []string
	Types          []fieldType
}

// specificMethods are generated according to specific templates and thus
// must not be wrapped automatically.
var specificMethods = map[string]bool{
	"Create": true,
	"Search": true,
	"First":  true,
	"All":    true,
}

// GeneratePool generates source code files inside the given directory for all models.
//
// GeneratePool works by reflection and cannot infer the names of the parameters of
// each methods. That's why we need to pass it a map with the method's AST data. Such
// a map can be generated by generate.GetMethodsASTData()
func GeneratePool(dir string, astData map[generate.MethodRef]generate.MethodASTData) {
	// We need to simulate bootstrapping to get embedded and mixed in fields
	createModelLinks()
	inflateMixIns()
	inflateEmbeddings()
	generateMethodsDoc()
	models := make([]string, len(Registry.registryByName))
	i := 0
	// Now we can generate pool for each model
	for modelName, mi := range Registry.registryByName {
		fileName := fmt.Sprintf("%s.go", strings.ToLower(modelName))
		generateModelPoolFile(mi, path.Join(dir, fileName), astData)
		models[i] = modelName
		i++
	}
}

// generateModelPoolFile generates the file with the source code of the
// pool object for the given Model.
func generateModelPoolFile(model *Model, fileName string, astData map[generate.MethodRef]generate.MethodASTData) {
	// Generate model data
	deps := map[string]bool{
		generate.PoolPath: true,
	}
	mData := modelData{
		Name:           model.name,
		Deps:           []string{generate.ModelsPath},
		ConditionFuncs: []string{"And", "AndNot", "Or", "OrNot"},
	}
	// Add fields
	addFieldsToModelData(&mData, model, &deps)
	// add Field types
	addFieldTypesToModelData(&mData, model)
	// Add methods
	addMethodsToModelData(&mData, model, astData, &deps)
	// Create file
	generate.CreateFileFromTemplate(fileName, defsFileTemplate, mData)
	log.Info("Generated pool source file for model", "model", model.name, "fileName", fileName)
}

// sanitizeFieldType returns the sanitizes name of the type
// and a bool value that is true if the type is a RecordSet
func sanitizedFieldType(mi *Model, typ reflect.Type) (string, bool) {
	var isRC bool
	typStr := typ.String()
	if typ == reflect.TypeOf(RecordCollection{}) {
		isRC = true
		typStr = fmt.Sprintf("%sSet", mi.name)
	}
	return strings.Replace(typStr, "pool.", "", 1), isRC
}

// addDependency adds the given type's path as dependency to the given modelData
// if not already imported. deps is the map of already imported paths and is updated
// by this function.
func addDependency(data *modelData, typ reflect.Type, deps *map[string]bool) {
	switch typ.Kind() {
	case reflect.Ptr:
		typ = typ.Elem()
	case reflect.Slice:
		el := typ.Elem()
		if typ.Name() == fmt.Sprintf("[]%s", el.Name()) {
			typ = el
		}
	case reflect.Map:
		el := typ.Elem()
		if strings.Contains(typ.Name(), el.Name()) {
			typ = el
		}
	}
	fDep := typ.PkgPath()
	if fDep != "" && !(*deps)[fDep] {
		data.Deps = append(data.Deps, fDep)
	}
	(*deps)[fDep] = true
}

// addFieldsToModelData adds the fields of the given Model to the given modelData.
// deps is the map of already imported paths and is updated by this function.
func addFieldsToModelData(mData *modelData, mi *Model, deps *map[string]bool) {
	for fieldName, fi := range mi.fields.registryByName {
		var (
			typStr   string
			typIsRS  bool
			relModel string
		)
		if fi.isRelationField() {
			typStr = fmt.Sprintf("%sSet", fi.relatedModelName)
			typIsRS = true
			relModel = fi.relatedModelName
		} else {
			typStr, _ = sanitizedFieldType(mi, fi.structField.Type)
		}
		mData.Fields = append(mData.Fields, fieldData{
			Name:         fieldName,
			RelModel:     relModel,
			IsSearchable: fi.isStored() || fi.isRelatedField(),
			Type:         typStr,
			SanType:      createTypeIdent(typStr),
			TypeIsRS:     typIsRS,
		})
		// Add dependency for this field, if needed and not already imported
		addDependency(mData, fi.structField.Type, deps)
	}
}

// createTypeIdent creates a string from the given type that
// can be used inside an identifier.
func createTypeIdent(typStr string) string {
	res := strings.Replace(typStr, ".", "", -1)
	res = strings.Replace(res, "[", "Slice", -1)
	res = strings.Replace(res, "map[", "Map", -1)
	res = strings.Replace(res, "]", "", -1)
	res = strings.Title(res)
	return res
}

// addFieldsToModelData extracts field types from mData.Fields
// and add them to mData.Types
func addFieldTypesToModelData(mData *modelData, model *Model) {
	fTypes := make(map[string]bool)
	for _, f := range mData.Fields {
		if fTypes[f.Type] {
			continue
		}
		fTypes[f.Type] = true
		mData.Types = append(mData.Types, fieldType{
			Type:     f.Type,
			SanType:  createTypeIdent(f.Type),
			TypeIsRS: f.TypeIsRS,
			Operators: []operatorDef{
				{Name: "Equals"}, {Name: "NotEquals"}, {Name: "Greater"}, {Name: "GreaterOrEqual"}, {Name: "Lower"},
				{Name: "LowerOrEqual"}, {Name: "LikePattern"}, {Name: "Like"}, {Name: "NotLike"}, {Name: "ILike"},
				{Name: "NotILike"}, {Name: "ILikePattern"}, {Name: "In", Multi: true}, {Name: "NotIn", Multi: true},
				{Name: "ChildOf"},
			},
		})
	}
}

// addMethodsToModelData adds the methods of the given Model to the given modelData.
// astData is a map of MethodASTData from which to get doc strings and params names.
// deps is the map of already imported paths and is updated by this function.
func addMethodsToModelData(mData *modelData, mi *Model, astData map[generate.MethodRef]generate.MethodASTData, deps *map[string]bool) {
	for methodName, methInfo := range mi.methods.registry {
		if specificMethods[methodName] {
			continue
		}

		ref := generate.MethodRef{Model: mi.name, Method: methodName}
		dParams, ok := astData[ref]
		if !ok {
			// Check if we have the method in mixins
			allMixIns := append(Registry.commonMixins, mi.mixins...)
			var mixInMethFound bool
			for i := len(allMixIns) - 1; i >= 0; i-- {
				mixInRef := generate.MethodRef{Model: allMixIns[i].name, Method: methodName}
				dParams, ok = astData[mixInRef]
				if ok {
					mixInMethFound = true
					break
				}
			}
			// Else we suppose it's a method generated in 'yep/models' and doesn't have a model set
			if !mixInMethFound {
				newRef := generate.MethodRef{Model: "", Method: methodName}
				dParams = astData[newRef]
			}
		}

		methType := methInfo.methodType
		params, paramsType := processMethodParameters(methType, mi, dParams, mData, deps)
		returns, returnString, returnAsserts, call := processMethodReturns(methType, mi, mData, deps)

		methData := methodData{
			Name:           methodName,
			Doc:            methInfo.doc,
			Params:         strings.Join(params, ", "),
			ParamsWithType: strings.Join(paramsType, ", "),
			Returns:        strings.Join(returns, ","),
			ReturnString:   strings.Join(returnString, ","),
			ReturnAsserts:  strings.Join(returnAsserts, "\n"),
			Call:           call,
		}
		mData.Methods = append(mData.Methods, methData)
	}
}

// processMethodParameters returns a list of parameter names and parameter types for the given method
func processMethodParameters(methType reflect.Type, m *Model, dParams generate.MethodASTData, mData *modelData, deps *map[string]bool) ([]string, []string) {
	params := make([]string, methType.NumIn()-1)
	paramsType := make([]string, methType.NumIn()-1)
	for i := 0; i < methType.NumIn()-1; i++ {
		var (
			varArgType, pType string
			isRC              bool
		)
		if methType.IsVariadic() && i == methType.NumIn()-2 {
			varArgType, isRC = sanitizedFieldType(m, methType.In(i+1).Elem())
			pType = fmt.Sprintf("...%s", varArgType)
		} else {
			pType, isRC = sanitizedFieldType(m, methType.In(i+1))
		}
		paramsType[i] = fmt.Sprintf("%s %s", dParams.Params[i], pType)
		if isRC {
			params[i] = fmt.Sprintf("%s.RecordCollection", dParams.Params[i])
		} else {
			params[i] = dParams.Params[i]
		}
		addDependency(mData, methType.In(i+1), deps)
	}
	return params, paramsType
}

// processMethodReturns returns the types and assert statements needed to generate the method
func processMethodReturns(methType reflect.Type, mi *Model, mData *modelData, deps *map[string]bool) ([]string, []string, []string, string) {
	returns := make([]string, methType.NumOut())
	returnString := make([]string, methType.NumOut())
	returnAsserts := make([]string, methType.NumOut())
	var call string
	if methType.NumOut() == 1 {
		typ, isRS := sanitizedFieldType(mi, methType.Out(0))
		addDependency(mData, methType.Out(0), deps)
		if isRS {
			returnAsserts[0] = "resTyped, _ := res.(models.RecordCollection)"
			returns[0] = fmt.Sprintf("%s{RecordCollection: resTyped}", typ)
		} else {
			returnAsserts[0] = fmt.Sprintf("resTyped, _ := res.(%s)", typ)
			returns[0] = "resTyped"
		}
		returnString[0] = typ
		call = "Call"
	} else if methType.NumOut() > 1 {
		for i := 0; i < methType.NumOut(); i++ {
			typ, isRS := sanitizedFieldType(mi, methType.Out(i))
			addDependency(mData, methType.Out(i), deps)
			if isRS {
				returnAsserts[i] = fmt.Sprintf("resTyped%d, _ := res[%d].(models.RecordCollection)", i, i)
				returns[i] = fmt.Sprintf("%s{RecordCollection: resTyped%d}", typ, i)
			} else {
				returnAsserts[i] = fmt.Sprintf("resTyped%d, _ := res[%d].(%s)", i, i, typ)
				returns[i] = fmt.Sprintf("resTyped%d", i)
			}
			returnString[i] = typ
		}
		call = "CallMulti"
	}
	return returns, returnString, returnAsserts, call
}

var defsFileTemplate = template.Must(template.New("").Parse(`
// This file is autogenerated by yep-generate
// DO NOT MODIFY THIS FILE - ANY CHANGES WILL BE OVERWRITTEN

package pool

import (
{{ range .Deps }} 	"{{ . }}"
{{ end }}
)

// ------- MODEL ---------

// {{ .Name }}Model is a strongly typed model definition that is used
// to extend the {{ .Name }} model or to get a {{ .Name }}Set through
// its NewSet() function.
//
// To get the unique instance of this type, call {{ .Name }}().
type {{ .Name }}Model struct {
	*models.Model
}

// NewSet returns a new {{ .Name }}Set instance in the given Environment
func (m {{ .Name }}Model) NewSet(env models.Environment) {{ .Name }}Set {
	return {{ .Name }}Set{
		RecordCollection: env.Pool("{{ .Name }}"),
	}
}

// Create creates a new {{ .Name }} record and returns the newly created
// {{ .Name }}Set instance.
func (m {{ .Name }}Model) Create(env models.Environment, data interface{}) {{ .Name }}Set {
	return {{ .Name }}Set{
		RecordCollection: m.Model.Create(env, data),
	}
}

// Search searches the database and returns a new {{ .Name }}Set instance
// with the records found.
func (m {{ .Name }}Model) Search(env models.Environment, cond {{ .Name }}Condition) {{ .Name }}Set {
	return {{ .Name }}Set{
		RecordCollection: m.Model.Search(env, cond.Condition),
	}
}

{{ range .Fields }}
{{ if .TypeIsRS }}
// {{ .Name }}FilteredOn adds a condition with a table join on the given field and
// filters the result with the given condition
func (m {{ $.Name }}Model) {{ .Name }}FilteredOn(cond {{ .RelModel }}Condition) {{ $.Name }}Condition {
	return {{ $.Name }}Condition{
		Condition: m.FilteredOn("{{ .Name }}", cond.Condition),
	}
}
{{ end }}

// {{ .Name }} adds the "{{ .Name }}" field to the Condition
func (m {{ $.Name }}Model) {{ .Name }}() {{ $.Name }}{{ .SanType }}ConditionField {
	return {{ $.Name }}{{ .SanType }}ConditionField{
		ConditionField: m.Field("{{ .Name }}"),
	}
}

{{ end }}

// {{ .Name }} returns the unique instance of the {{ .Name }}Model type
// which is used to extend the {{ .Name }} model or to get a {{ .Name }}Set through
// its NewSet() function.
func {{ .Name }}() {{ .Name }}Model {
	return {{ .Name }}Model{
		Model: models.Registry.MustGet("{{ .Name }}"),
	}
}

// ------- CONDITION ---------

// A {{ .Name }}Condition is a type safe WHERE clause in an SQL query
type {{ .Name }}Condition struct {
	*models.Condition
}

{{ range .ConditionFuncs }}
// {{ . }} completes the current condition with a simple {{ . }} clause : c.{{ . }}().nextCond => c {{ . }} nextCond
func (c {{ $.Name }}Condition) {{ . }}() {{ $.Name }}ConditionStart {
	return {{ $.Name }}ConditionStart{
		ConditionStart: c.Condition.{{ . }}(),
	}
}

// {{ . }}Cond completes the current condition with the given cond as an {{ . }} clause
// between brackets : c.{{ . }}(cond) => c {{ . }} (cond)
func (c {{ $.Name }}Condition) {{ . }}Cond(cond {{ $.Name }}Condition) {{ $.Name }}Condition {
	return {{ $.Name }}Condition{
		Condition: c.Condition.{{ . }}Cond(cond.Condition),
	}
}
{{ end }}

// ------- CONDITION START ---------

// A {{ .Name }}ConditionStart is an object representing a Condition when
// we just added a logical operator (AND, OR, ...) and we are
// about to add a predicate.
type {{ .Name }}ConditionStart struct {
	*models.ConditionStart
}

{{ range .Fields }}
// {{ .Name }} adds the "{{ .Name }}" field to the Condition
func (cs {{ $.Name }}ConditionStart) {{ .Name }}() {{ $.Name }}{{ .SanType }}ConditionField {
	return {{ $.Name }}{{ .SanType }}ConditionField{
		ConditionField: cs.Field("{{ .Name }}"),
	}
}

{{ if .TypeIsRS }}
// {{ .Name }}FilteredOn adds a condition with a table join on the given field and
// filters the result with the given condition
func (cs {{ $.Name }}ConditionStart) {{ .Name }}FilteredOn(cond {{ .RelModel }}Condition) {{ $.Name }}Condition {
	return {{ $.Name }}Condition{
		Condition: cs.FilteredOn("{{ .Name }}", cond.Condition),
	}
}
{{ end }}
{{ end }}

// ------- CONDITION FIELDS ----------

{{ range $typ := .Types }}
// A {{ $.Name }}{{ $typ.SanType }}ConditionField is a partial {{ $.Name }}Condition when
// we have selected a field of type {{ $typ.Type }} and expecting an operator.
type {{ $.Name }}{{ $typ.SanType }}ConditionField struct {
	*models.ConditionField
}

{{ range $typ.Operators }}
// {{ .Name }} adds a condition value to the ConditionPath
func (c {{ $.Name }}{{ $typ.SanType }}ConditionField) {{ .Name }}(arg {{ if and .Multi (not $typ.TypeIsRS) }}[]{{ end }}{{ $typ.Type }}) {{ $.Name }}Condition {
	return {{ $.Name }}Condition{
		Condition: c.ConditionField.{{ .Name }}(arg),
	}
}

// {{ .Name }}Func adds a function value to the ConditionPath.
// The function will be evaluated when the query is performed and
// it will be given the RecordSet on which the query is made as parameter
func (c {{ $.Name }}{{ $typ.SanType }}ConditionField) {{ .Name }}Func(arg func ({{ $.Name }}Set) {{ if and .Multi (not $typ.TypeIsRS) }}[]{{ end }}{{ $typ.Type }}) {{ $.Name }}Condition {
	return {{ $.Name }}Condition{
		Condition: c.ConditionField.{{ .Name }}(arg),
	}
}

{{ end }}
{{ end }}

// ------- DATA STRUCT ---------

// {{ .Name }}Data is an autogenerated struct type to handle {{ .Name }} data.
type {{ .Name }}Data struct {
{{ range .Fields }}	{{ .Name }} {{ .Type }}
{{ end }}
}

// ------- RECORD SET ---------

// {{ .Name }}Set is an autogenerated type to handle {{ .Name }} objects.
type {{ .Name }}Set struct {
	models.RecordCollection
}

var _ models.RecordSet = {{ .Name }}Set{}

// First returns a copy of the first Record of this RecordSet.
// It returns an empty {{ .Name }} if the RecordSet is empty.
func (s {{ .Name }}Set) First() {{ .Name }}Data {
	var res {{ .Name }}Data
	s.RecordCollection.First(&res)
	return res
}

// All Returns a copy of all records of the RecordCollection.
// It returns an empty slice if the RecordSet is empty.
func (s {{ .Name }}Set) All() []{{ .Name }}Data {
	var ptrSlice []*{{ .Name }}Data
	s.RecordCollection.All(&ptrSlice)
	res := make([]{{ .Name }}Data, len(ptrSlice))
	for i, ps := range ptrSlice {
		res[i] = *ps
	}
	return res
}

// Records returns a slice with all the records of this RecordSet, as singleton
// RecordSets
func (s {{ .Name }}Set) Records() []{{ .Name }}Set {
	res := make([]{{ .Name }}Set, len(s.RecordCollection.Records()))
	for i, rec := range s.RecordCollection.Records() {
		res[i] = {{ .Name }}Set{
			RecordCollection: rec,
		}
	}
	return res
}

// Create inserts a record in the database from the given {{ .Name }} data.
// Returns the created {{ .Name }}Set.
func (s {{ $.Name }}Set) Create(data *{{ .Name }}Data) {{ .Name }}Set {
	res := s.Call("Create", data)
	switch resTyped := res.(type) {
	case models.RecordCollection:
		return {{ .Name }}Set{RecordCollection: resTyped}
	case {{ .Name }}Set:
		return resTyped
	}
	return {{ .Name }}Set{}
}

// Search returns a new {{ $.Name }}Set filtering on the current one with the
// additional given Condition
func (s {{ $.Name }}Set) Search(condition {{ .Name }}Condition) {{ .Name }}Set {
	return {{ .Name }}Set{
		RecordCollection: s.RecordCollection.Search(condition.Condition),
	}
}

// Model returns an instance of {{ .Name }}Model
func (s {{ .Name }}Set) Model() {{ .Name }}Model {
	return {{ .Name }}Model{
		Model: s.RecordCollection.Model(),
	}
}

{{ range .Fields }}
// {{ .Name }} is a getter for the value of the "{{ .Name }}" field of the first
// record in this RecordSet. It returns the Go zero value if the RecordSet is empty.
func (s {{ $.Name }}Set) {{ .Name }}() {{ .Type }} {
{{ if .TypeIsRS }}	return {{ .Type }}{
		RecordCollection: s.RecordCollection.Get("{{ .Name }}").(models.RecordCollection),
	}{{ else -}}
	return s.RecordCollection.Get("{{ .Name }}").({{ .Type }}) {{ end }}
}

// Set{{ .Name }} is a setter for the value of the "{{ .Name }}" field of this
// RecordSet. All Records of this RecordSet will be updated. Each call to this
// method makes an update query in the database.
//
// Set{{ .Name }} panics if the RecordSet is empty.
func (s {{ $.Name }}Set) Set{{ .Name }}(value {{ .Type }}) {
	s.RecordCollection.Set("{{ .Name }}", value)
}
{{ end }}

// Super returns a RecordSet with a modified callstack so that call to the current
// method will execute the next method layer.
//
// This method is meant to be used inside a method layer function to call its parent,
// such as:
//
//    func (rs pool.MyRecordSet) MyMethod() string {
//        res := rs.Super().MyMethod()
//        res += " ok!"
//        return res
//    }
//
// Calls to a different method than the current method will call its next layer only
// if the current method has been called from a layer of the other method. Otherwise,
// it will be the same as calling the other method directly.
func (s {{ .Name }}Set) Super() {{ .Name }}Set {
	return {{ .Name }}Set{
		RecordCollection: s.RecordCollection.Super(),
	}
}

{{ range .Methods }}
{{ .Doc }}
func (s {{ $.Name }}Set) {{ .Name }}({{ .ParamsWithType }}) ({{ .ReturnString }}) {
{{- if eq .Returns "" }}
	s.Call("{{ .Name }}", {{ .Params}})
{{- else }}
	res := s.{{ .Call }}("{{ .Name }}", {{ .Params}})
	{{ .ReturnAsserts }}
	return {{ .Returns }}
{{- end }}
}

{{ end }}
`))
