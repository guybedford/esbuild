package api

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/evanw/esbuild/internal/ast"
	"github.com/evanw/esbuild/internal/bundler"
	"github.com/evanw/esbuild/internal/compat"
	"github.com/evanw/esbuild/internal/config"
	"github.com/evanw/esbuild/internal/fs"
	"github.com/evanw/esbuild/internal/lexer"
	"github.com/evanw/esbuild/internal/logging"
	"github.com/evanw/esbuild/internal/parser"
	"github.com/evanw/esbuild/internal/resolver"
)

func validatePlatform(value Platform) config.Platform {
	switch value {
	case PlatformBrowser:
		return config.PlatformBrowser
	case PlatformNode:
		return config.PlatformNode
	default:
		panic("Invalid platform")
	}
}

func validateFormat(value Format) config.Format {
	switch value {
	case FormatDefault:
		return config.FormatPreserve
	case FormatIIFE:
		return config.FormatIIFE
	case FormatCommonJS:
		return config.FormatCommonJS
	case FormatESModule:
		return config.FormatESModule
	default:
		panic("Invalid format")
	}
}

func validateSourceMap(value SourceMap) config.SourceMap {
	switch value {
	case SourceMapNone:
		return config.SourceMapNone
	case SourceMapLinked:
		return config.SourceMapLinkedWithComment
	case SourceMapInline:
		return config.SourceMapInline
	case SourceMapExternal:
		return config.SourceMapExternalWithoutComment
	default:
		panic("Invalid source map")
	}
}

func validateColor(value StderrColor) logging.StderrColor {
	switch value {
	case ColorIfTerminal:
		return logging.ColorIfTerminal
	case ColorNever:
		return logging.ColorNever
	case ColorAlways:
		return logging.ColorAlways
	default:
		panic("Invalid color")
	}
}

func validateLogLevel(value LogLevel) logging.LogLevel {
	switch value {
	case LogLevelInfo:
		return logging.LevelInfo
	case LogLevelWarning:
		return logging.LevelWarning
	case LogLevelError:
		return logging.LevelError
	default:
		panic("Invalid log level")
	}
}

func validateStrict(value StrictOptions) config.StrictOptions {
	return config.StrictOptions{
		NullishCoalescing: value.NullishCoalescing,
		ClassFields:       value.ClassFields,
	}
}

func validateLoader(value Loader) config.Loader {
	switch value {
	case LoaderNone:
		return config.LoaderNone
	case LoaderJS:
		return config.LoaderJS
	case LoaderJSX:
		return config.LoaderJSX
	case LoaderTS:
		return config.LoaderTS
	case LoaderTSX:
		return config.LoaderTSX
	case LoaderJSON:
		return config.LoaderJSON
	case LoaderText:
		return config.LoaderText
	case LoaderBase64:
		return config.LoaderBase64
	case LoaderDataURL:
		return config.LoaderDataURL
	case LoaderFile:
		return config.LoaderFile
	case LoaderBinary:
		return config.LoaderBinary
	default:
		panic("Invalid loader")
	}
}

func validateEngine(value EngineName) compat.Engine {
	switch value {
	case EngineChrome:
		return compat.Chrome
	case EngineEdge:
		return compat.Edge
	case EngineFirefox:
		return compat.Firefox
	case EngineIOS:
		return compat.IOS
	case EngineNode:
		return compat.Node
	case EngineSafari:
		return compat.Safari
	default:
		panic("Invalid loader")
	}
}

var versionRegex = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]+))?(?:\.([0-9]+))?$`)

func validateFeatures(log logging.Log, target Target, engines []Engine) compat.Feature {
	constraints := make(map[compat.Engine][]int)

	switch target {
	case ES5:
		constraints[compat.ES] = []int{5}
	case ES2015:
		constraints[compat.ES] = []int{2015}
	case ES2016:
		constraints[compat.ES] = []int{2016}
	case ES2017:
		constraints[compat.ES] = []int{2017}
	case ES2018:
		constraints[compat.ES] = []int{2018}
	case ES2019:
		constraints[compat.ES] = []int{2019}
	case ES2020:
		constraints[compat.ES] = []int{2020}
	case ESNext:
	default:
		panic("Invalid target")
	}

	for _, engine := range engines {
		if match := versionRegex.FindStringSubmatch(engine.Version); match != nil {
			if major, err := strconv.Atoi(match[1]); err == nil {
				version := []int{major}
				if minor, err := strconv.Atoi(match[2]); err == nil {
					version = append(version, minor)
				}
				if patch, err := strconv.Atoi(match[3]); err == nil {
					version = append(version, patch)
				}
				switch engine.Name {
				case EngineChrome:
					constraints[compat.Chrome] = version
				case EngineEdge:
					constraints[compat.Edge] = version
				case EngineFirefox:
					constraints[compat.Firefox] = version
				case EngineIOS:
					constraints[compat.IOS] = version
				case EngineNode:
					constraints[compat.Node] = version
				case EngineSafari:
					constraints[compat.Safari] = version
				default:
					panic("Invalid engine name")
				}
				continue
			}
		}

		log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid version: %q", engine.Version))
	}

	return compat.UnsupportedFeatures(constraints)
}

func validateExternals(log logging.Log, fs fs.FS, paths []string) config.ExternalModules {
	result := config.ExternalModules{
		NodeModules: make(map[string]bool),
		AbsPaths:    make(map[string]bool),
	}
	for _, path := range paths {
		if resolver.IsPackagePath(path) {
			result.NodeModules[path] = true
		} else if absPath := validatePath(log, fs, path); absPath != "" {
			result.AbsPaths[absPath] = true
		}
	}
	return result
}

func validateResolveExtensions(log logging.Log, order []string) []string {
	if order == nil {
		return []string{".tsx", ".ts", ".jsx", ".mjs", ".cjs", ".js", ".json"}
	}
	for _, ext := range order {
		if len(ext) < 2 || ext[0] != '.' {
			log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid file extension: %q", ext))
		}
	}
	return order
}

func validateLoaders(log logging.Log, loaders map[string]Loader) map[string]config.Loader {
	result := bundler.DefaultExtensionToLoaderMap()
	if loaders != nil {
		for ext, loader := range loaders {
			if len(ext) < 2 || ext[0] != '.' || ext[len(ext)-1] == '.' {
				log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid file extension: %q", ext))
			}
			result[ext] = validateLoader(loader)
		}
	}
	return result
}

func validateJSX(log logging.Log, text string, name string) []string {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ".")
	for _, part := range parts {
		if !lexer.IsIdentifier(part) {
			log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid JSX %s: %q", name, text))
			return nil
		}
	}
	return parts
}

func validateDefines(log logging.Log, defines map[string]string, pureFns []string) *config.ProcessedDefines {
	if len(defines) == 0 && len(pureFns) == 0 {
		return nil
	}

	rawDefines := make(map[string]config.DefineData)

	for key, value := range defines {
		// The key must be a dot-separated identifier list
		for _, part := range strings.Split(key, ".") {
			if !lexer.IsIdentifier(part) {
				log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid define key: %q", key))
				continue
			}
		}

		// Allow substituting for an identifier
		if lexer.IsIdentifier(value) {
			if _, ok := lexer.Keywords()[value]; !ok {
				name := value // The closure must close over a variable inside the loop
				rawDefines[key] = config.DefineData{
					DefineFunc: func(findSymbol config.FindSymbol) ast.E {
						return &ast.EIdentifier{Ref: findSymbol(name)}
					},
				}
				continue
			}
		}

		// Parse the value as JSON
		source := logging.Source{Contents: value}
		expr, ok := parser.ParseJSON(logging.NewDeferLog(), source, parser.ParseJSONOptions{})
		if !ok {
			log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid define value: %q", value))
			continue
		}

		// Only allow atoms for now
		var fn config.DefineFunc
		switch e := expr.Data.(type) {
		case *ast.ENull:
			fn = func(config.FindSymbol) ast.E { return &ast.ENull{} }
		case *ast.EBoolean:
			fn = func(config.FindSymbol) ast.E { return &ast.EBoolean{Value: e.Value} }
		case *ast.EString:
			fn = func(config.FindSymbol) ast.E { return &ast.EString{Value: e.Value} }
		case *ast.ENumber:
			fn = func(config.FindSymbol) ast.E { return &ast.ENumber{Value: e.Value} }
		default:
			log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid define value: %q", value))
			continue
		}

		rawDefines[key] = config.DefineData{DefineFunc: fn}
	}

	for _, key := range pureFns {
		// The key must be a dot-separated identifier list
		for _, part := range strings.Split(key, ".") {
			if !lexer.IsIdentifier(part) {
				log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid pure function: %q", key))
				continue
			}
		}

		// Merge with any previously-specified defines
		define := rawDefines[key]
		define.CallCanBeUnwrappedIfUnused = true
		rawDefines[key] = define
	}

	// Processing defines is expensive. Process them once here so the same object
	// can be shared between all parsers we create using these arguments.
	processed := config.ProcessDefines(rawDefines)
	return &processed
}

func validatePath(log logging.Log, fs fs.FS, relPath string) string {
	if relPath == "" {
		return ""
	}
	absPath, ok := fs.Abs(relPath)
	if !ok {
		log.AddError(nil, ast.Loc{}, fmt.Sprintf("Invalid path: %s", relPath))
	}
	return absPath
}

func convertMessagesToPublic(kind logging.MsgKind, msgs []logging.Msg) []Message {
	var filtered []Message
	for _, msg := range msgs {
		if msg.Kind == kind {
			var location *Location
			loc := msg.Location

			if loc != nil {
				location = &Location{
					File:     loc.File,
					Line:     loc.Line,
					Column:   loc.Column,
					Length:   loc.Length,
					LineText: loc.LineText,
				}
			}

			filtered = append(filtered, Message{
				Text:     msg.Text,
				Location: location,
			})
		}
	}
	return filtered
}

func convertMessagesToInternal(msgs []logging.Msg, kind logging.MsgKind, messages []Message) []logging.Msg {
	for _, message := range messages {
		var location *logging.MsgLocation
		loc := message.Location

		if loc != nil {
			location = &logging.MsgLocation{
				File:     loc.File,
				Line:     loc.Line,
				Column:   loc.Column,
				Length:   loc.Length,
				LineText: loc.LineText,
			}
		}

		msgs = append(msgs, logging.Msg{
			Kind:     kind,
			Text:     message.Text,
			Location: location,
		})
	}
	return msgs
}

////////////////////////////////////////////////////////////////////////////////
// Build API

func buildImpl(buildOpts BuildOptions) BuildResult {
	var log logging.Log
	if buildOpts.LogLevel == LogLevelSilent {
		log = logging.NewDeferLog()
	} else {
		log = logging.NewStderrLog(logging.StderrOptions{
			IncludeSource: true,
			ErrorLimit:    buildOpts.ErrorLimit,
			Color:         validateColor(buildOpts.Color),
			LogLevel:      validateLogLevel(buildOpts.LogLevel),
		})
	}

	// Convert and validate the buildOpts
	realFS := fs.RealFS()
	options := config.Options{
		UnsupportedFeatures: validateFeatures(log, buildOpts.Target, buildOpts.Engines),
		Strict:              validateStrict(buildOpts.Strict),
		JSX: config.JSXOptions{
			Factory:  validateJSX(log, buildOpts.JSXFactory, "factory"),
			Fragment: validateJSX(log, buildOpts.JSXFragment, "fragment"),
		},
		Defines:           validateDefines(log, buildOpts.Defines, buildOpts.PureFunctions),
		Platform:          validatePlatform(buildOpts.Platform),
		SourceMap:         validateSourceMap(buildOpts.Sourcemap),
		MangleSyntax:      buildOpts.MinifySyntax,
		RemoveWhitespace:  buildOpts.MinifyWhitespace,
		MinifyIdentifiers: buildOpts.MinifyIdentifiers,
		ModuleName:        buildOpts.GlobalName,
		IsBundling:        buildOpts.Bundle,
		CodeSplitting:     buildOpts.Splitting,
		OutputFormat:      validateFormat(buildOpts.Format),
		AbsOutputFile:     validatePath(log, realFS, buildOpts.Outfile),
		AbsOutputDir:      validatePath(log, realFS, buildOpts.Outdir),
		AbsMetadataFile:   validatePath(log, realFS, buildOpts.Metafile),
		ExtensionToLoader: validateLoaders(log, buildOpts.Loaders),
		ExtensionOrder:    validateResolveExtensions(log, buildOpts.ResolveExtensions),
		ExternalModules:   validateExternals(log, realFS, buildOpts.Externals),
	}
	entryPaths := make([]string, len(buildOpts.EntryPoints))
	for i, entryPoint := range buildOpts.EntryPoints {
		entryPaths[i] = validatePath(log, realFS, entryPoint)
	}
	entryPathCount := len(buildOpts.EntryPoints)
	if buildOpts.Stdin != nil {
		entryPathCount++
		options.Stdin = &config.StdinInfo{
			Loader:        validateLoader(buildOpts.Stdin.Loader),
			Contents:      buildOpts.Stdin.Contents,
			SourceFile:    buildOpts.Stdin.Sourcefile,
			AbsResolveDir: validatePath(log, realFS, buildOpts.Stdin.ResolveDir),
		}
	}

	if options.AbsOutputDir == "" && entryPathCount > 1 {
		log.AddError(nil, ast.Loc{},
			"Must use \"outdir\" when there are multiple input files")
	} else if options.AbsOutputDir == "" && options.CodeSplitting {
		log.AddError(nil, ast.Loc{},
			"Must use \"outdir\" when code splitting is enabled")
	} else if options.AbsOutputFile != "" && options.AbsOutputDir != "" {
		log.AddError(nil, ast.Loc{}, "Cannot use both \"outfile\" and \"outdir\"")
	} else if options.AbsOutputFile != "" {
		// If the output file is specified, use it to derive the output directory
		options.AbsOutputDir = realFS.Dir(options.AbsOutputFile)
	} else if options.AbsOutputDir == "" {
		options.WriteToStdout = true

		// Forbid certain features when writing to stdout
		if options.SourceMap != config.SourceMapNone && options.SourceMap != config.SourceMapInline {
			log.AddError(nil, ast.Loc{}, "Cannot use an external source map without an output path")
		}
		if options.AbsMetadataFile != "" {
			log.AddError(nil, ast.Loc{}, "Cannot use \"metafile\" without an output path")
		}
		for _, loader := range options.ExtensionToLoader {
			if loader == config.LoaderFile {
				log.AddError(nil, ast.Loc{}, "Cannot use the \"file\" loader without an output path")
				break
			}
		}

		// Use the current directory as the output directory instead of an empty
		// string because external modules with relative paths need a base directory.
		options.AbsOutputDir = realFS.Cwd()
	}

	if !options.IsBundling {
		// Disallow bundle-only options when not bundling
		if options.OutputFormat != config.FormatPreserve {
			log.AddError(nil, ast.Loc{}, "Cannot use \"format\" without \"bundle\"")
		}
		if len(options.ExternalModules.NodeModules) > 0 || len(options.ExternalModules.AbsPaths) > 0 {
			log.AddError(nil, ast.Loc{}, "Cannot use \"external\" without \"bundle\"")
		}
	} else if options.OutputFormat == config.FormatPreserve {
		// If the format isn't specified, set the default format using the platform
		switch options.Platform {
		case config.PlatformBrowser:
			options.OutputFormat = config.FormatIIFE
		case config.PlatformNode:
			options.OutputFormat = config.FormatCommonJS
		}
	}

	// Code splitting is experimental and currently only enabled for ES6 modules
	if options.CodeSplitting && options.OutputFormat != config.FormatESModule {
		log.AddError(nil, ast.Loc{}, "Splitting currently only works with the \"esm\" format")
	}

	loadPlugins(&options, log, buildOpts.Plugins)

	var outputFiles []OutputFile

	// Stop now if there were errors
	if !log.HasErrors() {
		// Scan over the bundle
		resolver := resolver.NewResolver(realFS, log, options)
		bundle := bundler.ScanBundle(log, realFS, resolver, entryPaths, options)

		// Stop now if there were errors
		if !log.HasErrors() {
			// Compile the bundle
			results := bundle.Compile(log, options)

			// Return the results
			outputFiles = make([]OutputFile, len(results))
			for i, result := range results {
				if options.WriteToStdout {
					result.AbsPath = "<stdout>"
				}
				outputFiles[i] = OutputFile{
					Path:     result.AbsPath,
					Contents: result.Contents,
				}
			}
		}
	}

	msgs := log.Done()
	return BuildResult{
		Errors:      convertMessagesToPublic(logging.Error, msgs),
		Warnings:    convertMessagesToPublic(logging.Warning, msgs),
		OutputFiles: outputFiles,
	}
}

////////////////////////////////////////////////////////////////////////////////
// Transform API

func transformImpl(input string, transformOpts TransformOptions) TransformResult {
	var log logging.Log
	if transformOpts.LogLevel == LogLevelSilent {
		log = logging.NewDeferLog()
	} else {
		log = logging.NewStderrLog(logging.StderrOptions{
			IncludeSource: true,
			ErrorLimit:    transformOpts.ErrorLimit,
			Color:         validateColor(transformOpts.Color),
			LogLevel:      validateLogLevel(transformOpts.LogLevel),
		})
	}

	// Apply default values
	if transformOpts.Sourcefile == "" {
		transformOpts.Sourcefile = "<stdin>"
	}
	if transformOpts.Loader == LoaderNone {
		transformOpts.Loader = LoaderJS
	}

	// Convert and validate the transformOpts
	options := config.Options{
		UnsupportedFeatures: validateFeatures(log, transformOpts.Target, transformOpts.Engines),
		Strict:              validateStrict(transformOpts.Strict),
		JSX: config.JSXOptions{
			Factory:  validateJSX(log, transformOpts.JSXFactory, "factory"),
			Fragment: validateJSX(log, transformOpts.JSXFragment, "fragment"),
		},
		Defines:           validateDefines(log, transformOpts.Defines, transformOpts.PureFunctions),
		SourceMap:         validateSourceMap(transformOpts.Sourcemap),
		MangleSyntax:      transformOpts.MinifySyntax,
		RemoveWhitespace:  transformOpts.MinifyWhitespace,
		MinifyIdentifiers: transformOpts.MinifyIdentifiers,
		AbsOutputFile:     transformOpts.Sourcefile + "-out",
		Stdin: &config.StdinInfo{
			Loader:     validateLoader(transformOpts.Loader),
			Contents:   input,
			SourceFile: transformOpts.Sourcefile,
		},
	}
	if options.SourceMap == config.SourceMapLinkedWithComment {
		// Linked source maps don't make sense because there's no output file name
		log.AddError(nil, ast.Loc{}, "Cannot transform with linked source maps")
	}
	if options.SourceMap != config.SourceMapNone && options.Stdin.SourceFile == "" {
		log.AddError(nil, ast.Loc{},
			"Must use \"sourcefile\" with \"sourcemap\" to set the original file name")
	}

	var results []bundler.OutputFile

	// Stop now if there were errors
	if !log.HasErrors() {
		// Scan over the bundle
		mockFS := fs.MockFS(make(map[string]string))
		resolver := resolver.NewResolver(mockFS, log, options)
		bundle := bundler.ScanBundle(log, mockFS, resolver, nil, options)

		// Stop now if there were errors
		if !log.HasErrors() {
			// Compile the bundle
			results = bundle.Compile(log, options)
		}
	}

	// Return the results
	var js []byte
	var jsSourceMap []byte

	// Unpack the JavaScript file and the source map file
	if len(results) == 1 {
		js = results[0].Contents
	} else if len(results) == 2 {
		a, b := results[0], results[1]
		if a.AbsPath == b.AbsPath+".map" {
			jsSourceMap, js = a.Contents, b.Contents
		} else if a.AbsPath+".map" == b.AbsPath {
			js, jsSourceMap = a.Contents, b.Contents
		}
	}

	msgs := log.Done()
	return TransformResult{
		Errors:      convertMessagesToPublic(logging.Error, msgs),
		Warnings:    convertMessagesToPublic(logging.Warning, msgs),
		JS:          js,
		JSSourceMap: jsSourceMap,
	}
}

////////////////////////////////////////////////////////////////////////////////
// Plugin API

type pluginImpl struct {
	log     logging.Log
	name    string
	loaders []config.LoaderPlugin
}

func (impl *pluginImpl) SetName(name string) {
	if name == "" {
		impl.log.AddError(nil, ast.Loc{}, "Name of plugin cannot be empty")
		return
	}

	if impl.name != "" {
		impl.log.AddError(nil, ast.Loc{}, "Name of plugin cannot be set multiple times")
		return
	}

	impl.name = name
}

var filterMutex sync.Mutex
var filterCache map[string]*regexp.Regexp

func compileFilter(filter string) (result *regexp.Regexp) {
	if filter == "" {
		// Must provide a filter
		return nil
	}
	ok := false

	// Cache hit?
	(func() {
		filterMutex.Lock()
		defer filterMutex.Unlock()
		if filterCache != nil {
			result, ok = filterCache[filter]
		}
	})()
	if ok {
		return
	}

	// Cache miss
	result, err := regexp.Compile(filter)
	if err != nil {
		return nil
	}

	// Cache for next time
	filterMutex.Lock()
	defer filterMutex.Unlock()
	if filterCache == nil {
		filterCache = make(map[string]*regexp.Regexp)
	}
	filterCache[filter] = result
	return
}

func (impl *pluginImpl) AddLoader(options LoaderOptions, callback func(LoaderArgs) (LoaderResult, error)) {
	if impl.name == "" {
		impl.log.AddError(nil, ast.Loc{}, "Set the plugin name before adding a loader")
		return
	}

	if options.Filter == "" {
		impl.log.AddError(nil, ast.Loc{}, fmt.Sprintf("[%s] Loader is missing a filter", impl.name))
		return
	}

	filter := compileFilter(options.Filter)
	if filter == nil {
		impl.log.AddError(nil, ast.Loc{},
			fmt.Sprintf("[%s] Loader filter is not a valid regular expression: %q", impl.name, options.Filter))
		return
	}

	impl.loaders = append(impl.loaders, config.LoaderPlugin{
		Name:          impl.name,
		Filter:        filter,
		MatchInternal: options.MatchInternal,
		Callback: func(args config.LoaderArgs) (result config.LoaderResult) {
			response, err := callback(LoaderArgs{
				Path: args.Path.String(),
			})

			if err != nil {
				result.LoaderError = err
				return
			}

			result.Contents = response.Contents
			result.Loader = validateLoader(response.Loader)

			// Convert log messages
			if len(response.Errors)+len(response.Warnings) > 0 {
				msgs := make(sortableMsgs, 0, len(response.Errors)+len(response.Warnings))
				msgs = convertMessagesToInternal(msgs, logging.Error, response.Errors)
				msgs = convertMessagesToInternal(msgs, logging.Warning, response.Warnings)
				sort.Sort(msgs)
				result.Msgs = msgs
			}
			return
		},
	})
}

// This type is just so we can use Go's native sort function
type sortableMsgs []logging.Msg

func (a sortableMsgs) Len() int          { return len(a) }
func (a sortableMsgs) Swap(i int, j int) { a[i], a[j] = a[j], a[i] }

func (a sortableMsgs) Less(i int, j int) bool {
	ai := a[i].Location
	aj := a[j].Location
	if ai == nil || aj == nil {
		return ai == nil && aj != nil
	}
	if ai.File != aj.File {
		return ai.File < aj.File
	}
	if ai.Line != aj.Line {
		return ai.Line < aj.Line
	}
	if ai.Column != aj.Column {
		return ai.Column < aj.Column
	}
	return a[i].Text < a[j].Text
}

func loadPlugins(options *config.Options, log logging.Log, plugins []func(Plugin)) {
	for _, item := range plugins {
		impl := &pluginImpl{log: log}
		item(impl)
		options.LoaderPlugins = append(options.LoaderPlugins, impl.loaders...)
	}
}
