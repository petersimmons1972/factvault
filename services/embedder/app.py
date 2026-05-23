from fastapi import FastAPI
from pydantic import BaseModel


class EmbedRequest(BaseModel):
    texts: list[str]


class EmbedResponse(BaseModel):
    vectors: list[list[float]]


app = FastAPI()


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/embed", response_model=EmbedResponse)
def embed(req: EmbedRequest) -> EmbedResponse:
    # Placeholder scaffold for Plan 1. Real model wiring lands in later plans.
    return EmbedResponse(vectors=[[0.0] * 1024 for _ in req.texts])

