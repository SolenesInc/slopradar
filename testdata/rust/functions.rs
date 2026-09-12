fn inspect(values: &[Option<i32>]) -> Result<i32, &'static str> {
    // comment-only line
    let normalize = |value: Option<i32>| if let Some(value) = value { value } else { 0 };
    if values.is_empty() || values.len() > 10 && values[0].is_some() {
        return Err("empty");
    }
    for value in values {
        while let Some(value) = value {
            match value {
                0 => continue,
                1..=9 => return Ok(*value),
                _ => break,
            }
        }
    }
    loop {
        return Ok(normalize(values[0]));
    }
}

struct Worker;

impl Worker {
    fn run(&self, value: Result<i32, &'static str>) -> Result<i32, &'static str> {
        let value = value?;
        Ok(value)
    }
}

#[cfg(test)]
mod tests {
    fn test_only() {
        assert!(true);
    }
}
