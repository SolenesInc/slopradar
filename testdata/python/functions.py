def inspect(values):
    """Inspect values without counting this docstring as SLOC."""
    # comment-only line
    def normalize(value):
        return value if value and value > 0 else 0

    if values and len(values) > 1 or not values:
        return [item if item else 0 for item in values if item >= 0]
    elif values:
        try:
            return next(item for item in values if item)
        except StopIteration:
            return None
    match values:
        case [first, *_]:
            return first
        case _:
            return None


class Worker:
    def run(self, values):
        while values:
            for value in values:
                if value:
                    return value
        return None


transform = lambda value: value if value else 0
