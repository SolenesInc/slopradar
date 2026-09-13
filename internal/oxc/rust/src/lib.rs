use std::{
    any::Any,
    ffi::c_int,
    panic::{AssertUnwindSafe, catch_unwind},
    path::Path,
    ptr,
};

use oxc_allocator::{Allocator, Vec as ArenaVec};
use oxc_ast::ast::*;
use oxc_ast_visit::{Visit, walk};
use oxc_parser::{ParseOptions, Parser, config::TokensParserConfig};
use oxc_span::{GetSpan, SourceType, Span};
use serde::Serialize;

const ABI_OK: c_int = 0;
const ABI_INVALID_INPUT: c_int = 1;
const ABI_PANIC: c_int = 2;

#[repr(C)]
pub struct SlopradarOxcBuffer {
    data: *mut u8,
    len: usize,
    capacity: usize,
}

impl Default for SlopradarOxcBuffer {
    fn default() -> Self {
        Self {
            data: ptr::null_mut(),
            len: 0,
            capacity: 0,
        }
    }
}

#[derive(Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
enum Status {
    Ok,
    ParseError,
}

#[derive(Debug, Serialize)]
struct Analysis {
    status: Status,
    diagnostics: Vec<Diagnostic>,
    functions: Vec<FunctionMetric>,
    comments: Vec<SourceRange>,
    tokens: Vec<Token>,
}

#[derive(Debug, Serialize)]
struct Diagnostic {
    message: String,
}

#[derive(Debug, Serialize, Clone, PartialEq)]
struct FunctionMetric {
    name: String,
    line: usize,
    cc: u64,
    sloc: usize,
    nested: Vec<FunctionMetric>,
}

#[derive(Debug, Serialize)]
struct SourceRange {
    start_byte: u32,
    end_byte: u32,
    start_line: usize,
    end_line: usize,
}

#[derive(Debug, Serialize)]
struct Token {
    text: String,
    line: usize,
}

#[unsafe(no_mangle)]
#[allow(clippy::missing_safety_doc)]
pub unsafe extern "C" fn slopradar_oxc_analyze(
    file_data: *const u8,
    file_len: usize,
    source_data: *const u8,
    source_len: usize,
    output: *mut SlopradarOxcBuffer,
) -> c_int {
    let Some(output) = (unsafe { output.as_mut() }) else {
        return ABI_INVALID_INPUT;
    };
    *output = SlopradarOxcBuffer::default();
    let (status, buffer) =
        caught_output(|| analyze_input(file_data, file_len, source_data, source_len));
    *output = buffer;
    status
}

fn caught_output(
    operation: impl FnOnce() -> Result<Vec<u8>, String>,
) -> (c_int, SlopradarOxcBuffer) {
    match catch_unwind(AssertUnwindSafe(operation)) {
        Ok(Ok(bytes)) => (ABI_OK, into_buffer(bytes)),
        Ok(Err(message)) => (ABI_INVALID_INPUT, into_buffer(message.into_bytes())),
        Err(payload) => (ABI_PANIC, into_buffer(panic_message(payload).into_bytes())),
    }
}

#[unsafe(no_mangle)]
#[allow(clippy::missing_safety_doc)]
pub unsafe extern "C" fn slopradar_oxc_release(buffer: *mut SlopradarOxcBuffer) {
    let Some(buffer) = (unsafe { buffer.as_mut() }) else {
        return;
    };
    if !buffer.data.is_null() {
        unsafe {
            drop(Vec::from_raw_parts(
                buffer.data,
                buffer.len,
                buffer.capacity,
            ));
        }
    }
    *buffer = SlopradarOxcBuffer::default();
}

fn analyze_input(
    file_data: *const u8,
    file_len: usize,
    source_data: *const u8,
    source_len: usize,
) -> Result<Vec<u8>, String> {
    let file = input(file_data, file_len, "file")?;
    let source = input(source_data, source_len, "source")?;
    let file = std::str::from_utf8(file).map_err(|error| format!("file is not UTF-8: {error}"))?;
    let source =
        std::str::from_utf8(source).map_err(|error| format!("source is not UTF-8: {error}"))?;
    serde_json::to_vec(&analyze_source(file, source)).map_err(|error| error.to_string())
}

fn input<'a>(data: *const u8, len: usize, name: &str) -> Result<&'a [u8], String> {
    if len == 0 {
        return Ok(&[]);
    }
    if data.is_null() {
        return Err(format!("{name} pointer is null with len={len}"));
    }
    Ok(unsafe { std::slice::from_raw_parts(data, len) })
}

fn into_buffer(mut bytes: Vec<u8>) -> SlopradarOxcBuffer {
    let buffer = SlopradarOxcBuffer {
        data: bytes.as_mut_ptr(),
        len: bytes.len(),
        capacity: bytes.capacity(),
    };
    std::mem::forget(bytes);
    buffer
}

fn panic_message(payload: Box<dyn Any + Send>) -> String {
    if let Some(message) = payload.downcast_ref::<&str>() {
        return (*message).to_string();
    }
    if let Some(message) = payload.downcast_ref::<String>() {
        return message.clone();
    }
    "native Oxc panic".to_string()
}

fn analyze_source(file: &str, source: &str) -> Analysis {
    let source_type = match SourceType::from_path(Path::new(file)) {
        Ok(source_type) => source_type,
        Err(error) => {
            return Analysis {
                status: Status::ParseError,
                diagnostics: vec![Diagnostic {
                    message: format!("unsupported source type: {error}"),
                }],
                functions: Vec::new(),
                comments: Vec::new(),
                tokens: Vec::new(),
            };
        }
    };

    let allocator = Allocator::default();
    let parsed = Parser::new(&allocator, source, source_type)
        .with_config(TokensParserConfig)
        .with_options(ParseOptions {
            parse_regular_expression: true,
            ..ParseOptions::default()
        })
        .parse();
    let diagnostics = parsed
        .diagnostics
        .iter()
        .map(|diagnostic| Diagnostic {
            message: diagnostic.to_string(),
        })
        .collect::<Vec<_>>();
    if parsed.fatal_error || !diagnostics.is_empty() {
        return Analysis {
            status: Status::ParseError,
            diagnostics,
            functions: Vec::new(),
            comments: Vec::new(),
            tokens: Vec::new(),
        };
    }

    let line_starts = line_starts(source);
    let comment_spans = parsed
        .program
        .hashbang
        .iter()
        .map(|hashbang| hashbang.span)
        .chain(parsed.program.comments.iter().map(|comment| comment.span))
        .collect::<Vec<_>>();
    let comments = comment_spans
        .iter()
        .map(|span| source_range(*span, &line_starts))
        .collect();
    let tokens = parsed
        .tokens
        .iter()
        .map(|token| Token {
            text: source[token.start() as usize..token.end() as usize].to_string(),
            line: line_of(token.start() as usize, &line_starts),
        })
        .collect::<Vec<_>>();
    let code_lines = code_lines(source, &comment_spans);
    let mut visitor = MetricVisitor::new(source, &line_starts, &code_lines);
    visitor.visit_program(&parsed.program);

    Analysis {
        status: Status::Ok,
        diagnostics,
        functions: visitor.roots,
        comments,
        tokens,
    }
}

fn line_starts(source: &str) -> Vec<usize> {
    let mut starts = vec![0];
    let bytes = source.as_bytes();
    let mut offset = 0;
    while offset < bytes.len() {
        let width = line_terminator_width(bytes, offset);
        if width == 0 {
            offset += 1;
        } else {
            offset += width;
            starts.push(offset);
        }
    }
    starts
}

fn line_terminator_width(source: &[u8], offset: usize) -> usize {
    match source[offset] {
        b'\n' => 1,
        b'\r' if source.get(offset + 1) == Some(&b'\n') => 2,
        b'\r' => 1,
        0xe2 if source.get(offset + 1) == Some(&0x80)
            && matches!(source.get(offset + 2), Some(0xa8 | 0xa9)) =>
        {
            3
        }
        _ => 0,
    }
}

fn line_of(byte: usize, starts: &[usize]) -> usize {
    starts.partition_point(|start| *start <= byte)
}

fn source_range(span: Span, starts: &[usize]) -> SourceRange {
    SourceRange {
        start_byte: span.start,
        end_byte: span.end,
        start_line: line_of(span.start as usize, starts),
        end_line: line_of(span.end.saturating_sub(1) as usize, starts),
    }
}

fn code_lines(source: &str, comments: &[Span]) -> Vec<bool> {
    let mut bytes = source.as_bytes().to_vec();
    for comment in comments {
        let mut offset = comment.start as usize;
        while offset < comment.end as usize {
            let width = line_terminator_width(&bytes, offset);
            if width == 0 {
                bytes[offset] = b' ';
                offset += 1;
            } else {
                offset += width;
            }
        }
    }
    let content = String::from_utf8(bytes).expect("masked source remains UTF-8");
    let bytes = content.as_bytes();
    let mut lines = Vec::new();
    let mut line_start = 0;
    loop {
        let mut line_end = line_start;
        while line_end < bytes.len() && line_terminator_width(bytes, line_end) == 0 {
            line_end += 1;
        }
        lines.push(
            content[line_start..line_end]
                .chars()
                .any(|character| !oxc_syntax::identifier::is_white_space(character)),
        );
        if line_end == bytes.len() {
            break;
        }
        line_start = line_end + line_terminator_width(bytes, line_end);
    }
    lines
}

struct FunctionFrame {
    name: String,
    span: Span,
    cc: u64,
    nested: Vec<FunctionMetric>,
}

struct MetricVisitor<'s> {
    source: &'s str,
    line_starts: &'s [usize],
    code_lines: &'s [bool],
    stack: Vec<FunctionFrame>,
    hints: Vec<String>,
    class_names: Vec<String>,
    namespaces: Vec<String>,
    roots: Vec<FunctionMetric>,
}

impl<'s> MetricVisitor<'s> {
    fn new(source: &'s str, line_starts: &'s [usize], code_lines: &'s [bool]) -> Self {
        Self {
            source,
            line_starts,
            code_lines,
            stack: Vec::new(),
            hints: Vec::new(),
            class_names: Vec::new(),
            namespaces: Vec::new(),
            roots: Vec::new(),
        }
    }

    fn start_function(&mut self, name: String, span: Span) {
        let name = if self.stack.is_empty() && !self.namespaces.is_empty() {
            format!("{}.{}", self.namespaces.join("."), name)
        } else {
            name
        };
        self.stack.push(FunctionFrame {
            name,
            span,
            cc: 1,
            nested: Vec::new(),
        });
    }

    fn finish_function(&mut self) {
        let frame = self.stack.pop().expect("function stack is balanced");
        let start_line = line_of(frame.span.start as usize, self.line_starts);
        let end_line = line_of(frame.span.end.saturating_sub(1) as usize, self.line_starts);
        let sloc = self.code_lines[start_line - 1..end_line]
            .iter()
            .filter(|has_code| **has_code)
            .count();
        let metric = FunctionMetric {
            name: frame.name,
            line: start_line,
            cc: frame.cc,
            sloc,
            nested: frame.nested,
        };
        if let Some(parent) = self.stack.last_mut() {
            parent.nested.push(metric);
        } else {
            self.roots.push(metric);
        }
    }

    fn decision(&mut self) {
        for frame in &mut self.stack {
            frame.cc += 1;
        }
    }

    fn hint(&self) -> String {
        self.hints
            .last()
            .cloned()
            .unwrap_or_else(|| "(anonymous)".to_string())
    }

    fn source_for(&self, span: Span) -> String {
        self.source[span.start as usize..span.end as usize].to_string()
    }

    fn name_for(&self, span: Span) -> String {
        self.source_for(span)
            .trim()
            .trim_matches(['"', '\'', '`'])
            .to_string()
    }

    fn callee_for(&self, span: Span) -> String {
        self.source_for(span)
            .chars()
            .filter(|character| !character.is_whitespace())
            .collect()
    }

    fn callback_name(&self, callee: String) -> String {
        match self.hints.last() {
            Some(owner) if owner != "(anonymous)" => format!("{owner}.cb:{callee}"),
            _ => format!("cb:{callee}"),
        }
    }

    fn lexical_owner(&self) -> Option<&str> {
        self.hints
            .last()
            .filter(|hint| hint.as_str() != "(anonymous)")
            .map(String::as_str)
    }

    fn class_name(&self, class: &Class<'_>) -> String {
        let owner = self.lexical_owner().or_else(|| {
            if self.stack.is_empty() {
                self.class_names.last().map(String::as_str)
            } else {
                None
            }
        });
        match (class.id.as_ref(), owner) {
            (Some(identifier), Some(owner)) => format!("{owner}.{}", identifier.name),
            (Some(identifier), None) => identifier.name.to_string(),
            (None, Some(owner)) => owner.to_string(),
            (None, None) => "(anonymous class)".to_string(),
        }
    }

    fn class_member_name(&self, name: String, r#static: bool, kind: Option<&str>) -> String {
        let mut member = String::new();
        if r#static {
            member.push_str("static ");
        }
        if let Some(kind) = kind {
            member.push_str(kind);
            member.push(' ');
        }
        member.push_str(&name);
        format!(
            "{}.{}",
            self.class_names
                .last()
                .expect("class members are visited inside a class"),
            member
        )
    }

    fn object_member_name(&self, name: String, kind: PropertyKind) -> String {
        let member = match kind {
            PropertyKind::Init => name,
            PropertyKind::Get => format!("get {name}"),
            PropertyKind::Set => format!("set {name}"),
        };
        match self.hints.last() {
            Some(owner) if owner != "(anonymous)" => {
                format!("{owner}.{member}")
            }
            _ => member,
        }
    }

    fn with_hint(&mut self, hint: String, visit: impl FnOnce(&mut Self)) {
        self.hints.push(hint);
        visit(self);
        self.hints.pop();
    }

    fn with_class_name(&mut self, name: String, visit: impl FnOnce(&mut Self)) {
        self.class_names.push(name);
        visit(self);
        self.class_names.pop();
    }
}

impl<'a> Visit<'a> for MetricVisitor<'_> {
    fn visit_ts_namespace_declaration(&mut self, namespace: &TSNamespaceDeclaration<'a>) {
        self.namespaces.push(namespace.id.name.to_string());
        walk::walk_ts_namespace_declaration(self, namespace);
        self.namespaces.pop();
    }

    fn visit_class(&mut self, class: &Class<'a>) {
        let name = self.class_name(class);
        self.with_class_name(name, |visitor| walk::walk_class(visitor, class));
    }

    fn visit_function(&mut self, function: &Function<'a>, flags: oxc_syntax::scope::ScopeFlags) {
        if function.body.is_none() {
            walk::walk_function(self, function, flags);
            return;
        }
        let name = function.id.as_ref().map_or_else(
            || self.hint(),
            |identifier| {
                if function.r#type == FunctionType::FunctionExpression
                    && let Some(owner) = self.lexical_owner()
                {
                    return format!("{owner}.{}", identifier.name);
                }
                identifier.name.to_string()
            },
        );
        self.start_function(name, function.span);
        self.with_hint("(anonymous)".to_string(), |visitor| {
            walk::walk_function(visitor, function, flags)
        });
        self.finish_function();
    }

    fn visit_arrow_function_expression(&mut self, function: &ArrowFunctionExpression<'a>) {
        self.start_function(self.hint(), function.span);
        self.with_hint("(anonymous)".to_string(), |visitor| {
            walk::walk_arrow_function_expression(visitor, function)
        });
        self.finish_function();
    }

    fn visit_variable_declarator(&mut self, declarator: &VariableDeclarator<'a>) {
        let hint = self.name_for(declarator.id.span());
        self.with_hint(hint, |visitor| {
            walk::walk_variable_declarator(visitor, declarator)
        });
    }

    fn visit_assignment_expression(&mut self, expression: &AssignmentExpression<'a>) {
        let hint = self.name_for(expression.left.span());
        self.with_hint(hint, |visitor| {
            walk::walk_assignment_expression(visitor, expression)
        });
    }

    fn visit_object_property(&mut self, property: &ObjectProperty<'a>) {
        let hint = self.object_member_name(self.name_for(property.key.span()), property.kind);
        self.with_hint(hint, |visitor| {
            walk::walk_object_property(visitor, property)
        });
    }

    fn visit_property_definition(&mut self, property: &PropertyDefinition<'a>) {
        let hint =
            self.class_member_name(self.name_for(property.key.span()), property.r#static, None);
        self.with_hint(hint, |visitor| {
            walk::walk_property_definition(visitor, property)
        });
    }

    fn visit_accessor_property(&mut self, property: &AccessorProperty<'a>) {
        let hint = self.class_member_name(
            self.name_for(property.key.span()),
            property.r#static,
            Some("accessor"),
        );
        self.with_hint(hint, |visitor| {
            walk::walk_accessor_property(visitor, property)
        });
    }

    fn visit_method_definition(&mut self, method: &MethodDefinition<'a>) {
        let (name, kind) = match method.kind {
            MethodDefinitionKind::Constructor => ("constructor".to_string(), None),
            MethodDefinitionKind::Method => (self.name_for(method.key.span()), None),
            MethodDefinitionKind::Get => (self.name_for(method.key.span()), Some("get")),
            MethodDefinitionKind::Set => (self.name_for(method.key.span()), Some("set")),
        };
        let hint = self.class_member_name(name, method.r#static, kind);
        self.with_hint(hint, |visitor| {
            walk::walk_method_definition(visitor, method)
        });
    }

    fn visit_array_expression(&mut self, array: &ArrayExpression<'a>) {
        let owner = self.hint();
        for (index, element) in array.elements.iter().enumerate() {
            let hint = if owner == "(anonymous)" {
                format!("[{index}]")
            } else {
                format!("{owner}[{index}]")
            };
            self.with_hint(hint, |visitor| {
                visitor.visit_array_expression_element(element)
            });
        }
    }

    fn visit_arguments(&mut self, arguments: &ArenaVec<'a, Argument<'a>>) {
        let owner = self.hint();
        for (index, argument) in arguments.iter().enumerate() {
            let hint = if arguments.len() > 1 {
                format!("{owner}[{index}]")
            } else {
                owner.clone()
            };
            self.with_hint(hint, |visitor| visitor.visit_argument(argument));
        }
    }

    fn visit_call_expression(&mut self, call: &CallExpression<'a>) {
        let hint = self.callback_name(self.callee_for(call.callee.span()));
        self.visit_expression(&call.callee);
        if let Some(arguments) = &call.type_arguments {
            self.visit_ts_type_parameter_instantiation(arguments);
        }
        self.with_hint(hint, |visitor| visitor.visit_arguments(&call.arguments));
    }

    fn visit_new_expression(&mut self, expression: &NewExpression<'a>) {
        let hint = self.callback_name(self.callee_for(expression.callee.span()));
        self.visit_expression(&expression.callee);
        if let Some(arguments) = &expression.type_arguments {
            self.visit_ts_type_parameter_instantiation(arguments);
        }
        self.with_hint(hint, |visitor| {
            visitor.visit_arguments(&expression.arguments)
        });
    }

    fn visit_if_statement(&mut self, statement: &IfStatement<'a>) {
        self.decision();
        walk::walk_if_statement(self, statement);
    }

    fn visit_do_while_statement(&mut self, statement: &DoWhileStatement<'a>) {
        self.decision();
        walk::walk_do_while_statement(self, statement);
    }

    fn visit_while_statement(&mut self, statement: &WhileStatement<'a>) {
        self.decision();
        walk::walk_while_statement(self, statement);
    }

    fn visit_for_statement(&mut self, statement: &ForStatement<'a>) {
        self.decision();
        walk::walk_for_statement(self, statement);
    }

    fn visit_for_in_statement(&mut self, statement: &ForInStatement<'a>) {
        self.decision();
        walk::walk_for_in_statement(self, statement);
    }

    fn visit_for_of_statement(&mut self, statement: &ForOfStatement<'a>) {
        self.decision();
        walk::walk_for_of_statement(self, statement);
    }

    fn visit_catch_clause(&mut self, clause: &CatchClause<'a>) {
        self.decision();
        walk::walk_catch_clause(self, clause);
    }

    fn visit_switch_case(&mut self, case: &SwitchCase<'a>) {
        if case.test.is_some() {
            self.decision();
        }
        walk::walk_switch_case(self, case);
    }

    fn visit_conditional_expression(&mut self, expression: &ConditionalExpression<'a>) {
        self.decision();
        walk::walk_conditional_expression(self, expression);
    }

    fn visit_logical_expression(&mut self, expression: &LogicalExpression<'a>) {
        self.decision();
        walk::walk_logical_expression(self, expression);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn callback_names_retain_enclosing_owners() {
        let analysis = analyze_source(
            "owners.ts",
            "const first = map(() => {}); const second = map(() => {}); assigned.target = new Promise(() => {}); const nested = outer(inner(() => {})); function f() { const local = map(() => () => 1); }",
        );
        assert_eq!(analysis.status, Status::Ok);
        let names = analysis
            .functions
            .iter()
            .map(|f| f.name.as_str())
            .collect::<Vec<_>>();
        assert_eq!(
            names,
            [
                "first.cb:map",
                "second.cb:map",
                "assigned.target.cb:Promise",
                "nested.cb:outer.cb:inner",
                "f"
            ]
        );
        assert_eq!(analysis.functions[4].nested[0].name, "local.cb:map");
        assert_eq!(
            analysis.functions[4].nested[0].nested[0].name,
            "(anonymous)"
        );
    }

    #[test]
    fn direct_callback_values_keep_the_call_owner() {
        let analysis = analyze_source(
            "arguments.ts",
            "register({ run() {} }); install({ run() {} }); register(class Shared { run() {} }); install(class Shared { run() {} }); register(function shared() {}); install(function shared() {}); register({ nested: { run() {} } }); register({ run() { const local = () => {}; } });",
        );
        assert_eq!(analysis.status, Status::Ok);
        let names = analysis
            .functions
            .iter()
            .map(|f| f.name.as_str())
            .collect::<Vec<_>>();
        assert_eq!(
            names,
            [
                "cb:register.run",
                "cb:install.run",
                "cb:register.Shared.run",
                "cb:install.Shared.run",
                "cb:register.shared",
                "cb:install.shared",
                "cb:register.nested.run",
                "cb:register.run"
            ]
        );
        assert_eq!(analysis.functions[7].nested[0].name, "local");
    }

    #[test]
    fn ecmascript_whitespace_does_not_add_function_lines() {
        for whitespace in [
            '\u{a0}', '\u{1680}', '\u{2000}', '\u{202f}', '\u{205f}', '\u{3000}', '\u{feff}',
        ] {
            let source = format!("function f() {{\n{whitespace}\nreturn 1;\n}}");
            let analysis = analyze_source("space.js", &source);
            assert_eq!(analysis.status, Status::Ok);
            assert_eq!(analysis.functions[0].sloc, 3, "{whitespace:?}");
        }
        let analysis = analyze_source("literal.js", "function f() { return `\n\u{85}\n`; }");
        assert_eq!(analysis.status, Status::Ok);
        assert_eq!(analysis.functions[0].sloc, 3);
    }

    #[test]
    fn named_class_expressions_keep_outer_ownership() {
        let analysis = analyze_source(
            "classes.ts",
            "class OuterA { static child = class Inner { run() {} }; static { class Local { run() {} } } } class OuterB { static child = class Inner { run() {} } } const First = class Shared { run() {} }; const Second = class Shared { run() {} };",
        );
        assert_eq!(analysis.status, Status::Ok);
        let names = analysis
            .functions
            .iter()
            .map(|f| f.name.as_str())
            .collect::<Vec<_>>();
        assert_eq!(
            names,
            [
                "OuterA.static child.Inner.run",
                "OuterA.Local.run",
                "OuterB.static child.Inner.run",
                "First.Shared.run",
                "Second.Shared.run"
            ]
        );
    }

    #[test]
    fn named_function_expressions_keep_assignment_owners() {
        let analysis = analyze_source(
            "functions.ts",
            "const first = function shared() {}; const second = function shared() {}; function outer() { const local = function shared() {}; function declared() {} }",
        );
        assert_eq!(analysis.status, Status::Ok);
        assert_eq!(analysis.functions[0].name, "first.shared");
        assert_eq!(analysis.functions[1].name, "second.shared");
        assert_eq!(analysis.functions[2].name, "outer");
        assert_eq!(analysis.functions[2].nested[0].name, "local.shared");
        assert_eq!(analysis.functions[2].nested[1].name, "declared");
    }

    #[test]
    fn collections_and_callback_arguments_keep_positions() {
        let analysis = analyze_source(
            "collections.ts",
            "const items = [() => 1, [() => 2, () => 3]]; const owned = combine(() => 1, () => 2); const immediate = (() => 1)(); const created = new Factory(() => 1, () => 2);",
        );
        assert_eq!(analysis.status, Status::Ok);
        let names = analysis
            .functions
            .iter()
            .map(|f| f.name.as_str())
            .collect::<Vec<_>>();
        assert_eq!(
            names,
            [
                "items[0]",
                "items[1][0]",
                "items[1][1]",
                "owned.cb:combine[0]",
                "owned.cb:combine[1]",
                "immediate",
                "created.cb:Factory[0]",
                "created.cb:Factory[1]"
            ]
        );
    }

    #[test]
    fn counts_every_function_span() {
        let analysis = analyze_source(
            "valid.ts",
            "function outer(a: boolean) { if (a) { const inner = () => { while (a) {} }; } }",
        );
        assert_eq!(analysis.status, Status::Ok);
        assert_eq!(analysis.functions[0].cc, 3);
        assert_eq!(analysis.functions[0].nested[0].cc, 2);
    }

    #[test]
    fn uses_assignment_and_full_callback_names() {
        let analysis = analyze_source(
            "valid.ts",
            "target.handler = () => {}; ({ 'quoted': () => {} }); extremelyLongCallbackCalleeNameThatMustRemainVisible(() => {}); new Promise(() => {});",
        );
        assert_eq!(analysis.functions[0].name, "target.handler");
        assert_eq!(analysis.functions[1].name, "quoted");
        assert_eq!(
            analysis.functions[2].name,
            "cb:extremelyLongCallbackCalleeNameThatMustRemainVisible"
        );
        assert_eq!(analysis.functions[3].name, "cb:Promise");
    }

    #[test]
    fn consumes_ownership_hints_at_function_boundaries() {
        let analysis = analyze_source(
            "valid.ts",
            "const outer = () => () => 1; const parent = () => { const child = () => 2; return child; }; factory(() => () => 3);",
        );
        assert_eq!(analysis.functions[0].name, "outer");
        assert_eq!(analysis.functions[0].nested[0].name, "(anonymous)");
        assert_eq!(analysis.functions[1].name, "parent");
        assert_eq!(analysis.functions[1].nested[0].name, "child");
        assert_eq!(analysis.functions[2].name, "cb:factory");
        assert_eq!(analysis.functions[2].nested[0].name, "(anonymous)");
    }

    #[test]
    fn qualifies_class_members() {
        let analysis = analyze_source(
            "valid.ts",
            "class A { run() {} static build() {} get value() { return 1 } set value(next: number) {} task = () => () => {}; static boot = () => {}; } const Assigned = class { run() {} }; const Alias = class Internal { run() {} }; factory(class { run() {} }); class Outer { method() { class Inner { run() {} } return () => {}; } }",
        );
        let names = analysis
            .functions
            .iter()
            .map(|function| function.name.as_str())
            .collect::<Vec<_>>();
        assert_eq!(
            names,
            [
                "A.run",
                "A.static build",
                "A.get value",
                "A.set value",
                "A.task",
                "A.static boot",
                "Assigned.run",
                "Alias.Internal.run",
                "cb:factory.run",
                "Outer.method",
            ]
        );
        assert_eq!(analysis.functions[4].nested[0].name, "(anonymous)");
        assert_eq!(analysis.functions[9].nested[0].name, "Inner.run");
        assert_eq!(analysis.functions[9].nested[1].name, "(anonymous)");
    }

    #[test]
    fn qualifies_object_literal_members() {
        let analysis = analyze_source(
            "valid.ts",
            "const owned = { run() {}, arrow: () => {}, functionValue: function() {}, get value() { return 1 }, set value(next: number) {}, nested: { run() {} }, explicit: function retained() {} }; assigned.target = { run() {}, nested: { arrow: () => {} } }; factory({ run() {}, arrow: () => {} }); function outer() { return { run() {} }; }",
        );
        let names = analysis
            .functions
            .iter()
            .map(|function| function.name.as_str())
            .collect::<Vec<_>>();
        assert_eq!(
            names,
            [
                "owned.run",
                "owned.arrow",
                "owned.functionValue",
                "owned.get value",
                "owned.set value",
                "owned.nested.run",
                "owned.explicit.retained",
                "assigned.target.run",
                "assigned.target.nested.arrow",
                "cb:factory.run",
                "cb:factory.arrow",
                "outer",
            ]
        );
        assert_eq!(analysis.functions[11].nested[0].name, "run");
    }

    #[test]
    fn recognizes_javascript_line_terminators() {
        for terminator in ["\n", "\r\n", "\r", "\u{2028}", "\u{2029}"] {
            let source = [
                "function f(x) {",
                "  // removed comment",
                "  if (x) return 1;",
                "  return 0;",
                "}",
            ]
            .join(terminator);
            let analysis = analyze_source("valid.js", &source);
            assert_eq!(analysis.status, Status::Ok, "terminator {terminator:?}");
            assert_eq!(analysis.functions.len(), 1, "terminator {terminator:?}");
            assert_eq!(analysis.functions[0].line, 1, "terminator {terminator:?}");
            assert_eq!(analysis.functions[0].sloc, 4, "terminator {terminator:?}");
            assert_eq!(
                analysis.comments[0].start_line, 2,
                "terminator {terminator:?}"
            );
            assert_eq!(
                analysis.comments[0].end_line, 2,
                "terminator {terminator:?}"
            );
            assert!(
                analysis
                    .tokens
                    .iter()
                    .any(|token| token.text == "if" && token.line == 3),
                "terminator {terminator:?}"
            );
            assert!(
                analysis
                    .tokens
                    .iter()
                    .any(|token| token.text == "0" && token.line == 4),
                "terminator {terminator:?}"
            );
        }
    }

    #[test]
    fn refuses_metrics_when_parser_reports_diagnostics() {
        let analysis = analyze_source("invalid.ts", "export function broken( {");
        assert_eq!(analysis.status, Status::ParseError);
        assert!(!analysis.diagnostics.is_empty());
        assert!(analysis.functions.is_empty());
        assert!(analysis.tokens.is_empty());
    }

    #[test]
    fn extracts_original_tokens_without_comments() {
        let analysis = analyze_source("valid.ts", "function run() { /* marker */ return 1; }");
        assert!(analysis.tokens.iter().any(|token| token.text == "run"));
        assert!(analysis.tokens.iter().all(|token| token.text != "marker"));
        assert_eq!(analysis.comments.len(), 1);
    }

    #[test]
    fn catches_panics_before_the_abi_boundary() {
        let (status, mut buffer) = caught_output(|| panic!("boundary failure"));
        assert_eq!(status, ABI_PANIC);
        let message = unsafe { std::slice::from_raw_parts(buffer.data, buffer.len) };
        assert_eq!(message, b"boundary failure");
        unsafe { slopradar_oxc_release(&mut buffer) };
        assert!(buffer.data.is_null());
        assert_eq!(buffer.len, 0);
        assert_eq!(buffer.capacity, 0);
    }
}

#[cfg(test)]
#[test]
fn namespace_owners_preserve_local_function_boundaries() {
    let analysis = analyze_source(
        "namespaces.ts",
        "namespace A.B { export function run() { function local() {} } export const arrow = () => 1; export class C { run() {} } } namespace D { export function run() {} } function outside() {}",
    );
    assert_eq!(analysis.status, Status::Ok);
    let names: Vec<_> = analysis.functions.iter().map(|f| f.name.as_str()).collect();
    assert_eq!(
        names,
        ["A.B.run", "A.B.arrow", "A.B.C.run", "D.run", "outside"]
    );
    assert_eq!(analysis.functions[0].nested[0].name, "local");
}

#[cfg(test)]
#[test]
fn hashbang_is_a_comment_for_tokens_and_source_lines() {
    for terminator in ["\n", "\r\n", "\r", "\u{2028}", "\u{2029}"] {
        let source = format!("#!/usr/bin/env node{terminator}function f() {{}}");
        let analysis = analyze_source("example.mjs", &source);
        assert_eq!(analysis.status, Status::Ok);
        assert_eq!(analysis.comments.len(), 1);
        assert_eq!(analysis.comments[0].start_byte, 0);
        assert_eq!(analysis.comments[0].end_byte, 19);
        assert_eq!(analysis.functions[0].line, 2);
        assert_eq!(analysis.functions[0].sloc, 1);
        assert_eq!(analysis.tokens[0].line, 2);
    }
}
