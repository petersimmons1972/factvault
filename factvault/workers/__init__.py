# Auto-import all worker modules so their @register_worker decorators fire at CLI startup.
from factvault.workers import archive as _archive  # noqa: F401
from factvault.workers import verify as _verify  # noqa: F401
