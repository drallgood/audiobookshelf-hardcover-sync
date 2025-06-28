# Queries

## Book by ASIN

```graphql
query BookByASIN($asin: String!) {
  books(where: {editions: {asin: {_eq: $asin}, reading_format: {id: {_eq: 2}}}}, limit: 1) {
    id
    title
    audio_seconds
    book_status_id
    canonical_id
    editions(where: {asin: {_eq: $asin}, reading_format: {id: {_eq: 2}}}, limit: 1) {
      id
      asin
      isbn_13: isbn_13
      isbn_10: isbn_10
      title
      edition_format
      reading_format {
        id
      }
      audio_seconds
      cached_image(path: "url")
      publisher {
        id
        name
      }
      language {
        id
        language
      }
      image {
        id
        url
      }
    }
  }
}
```

```json
{
  "asin": "B007W4Z6TW"
}
```

## Book by ID

```graphql
query BookByID($id: Int!) {
  books(where: {id: {_eq: $id}}, limit: 1) {
    id
    title
    audio_seconds
    book_status_id
    canonical_id
    editions(where: {reading_format: {id: {_eq: 2}}}, limit: 1) {
      id
      asin
      isbn_13: isbn_13
      isbn_10: isbn_10
      title
      edition_format
      reading_format {
        id
      }
      audio_seconds
      cached_image(path: "url")
      publisher {
        id
        name
      }
      language {
        id
        language
      }
      image {
        id
        url
      }
    }
  }
}
```

```json
{
  "id": 1
}
```

## Book by Title and Author

```graphql
query BookByTitleAndAuthor($title: String!, $author: String!) {
  books(where: {editions: {title: {_eq: $title}, reading_format: {id: {_eq: 2}}}}, limit: 1) {
    id
    title
    audio_seconds
    book_status_id
    canonical_id
    editions(where: {title: {_eq: $title}, reading_format: {id: {_eq: 2}}}, limit: 1) {
      id
      asin
      isbn_13: isbn_13
      isbn_10: isbn_10
      title
      edition_format
      reading_format {
        id
      }
      audio_seconds
      cached_image(path: "url")
      publisher {
        id
        name
      }
      language {
        id
        language
      }
      image {
        id
        url
      }
    }
  }
}
```

## Reads by ASIB

```graphql
query GetUserBookReadsForASIN($asin: String) {
  user_book_reads(where: {edition: {asin: {_eq: $asin}}}) {
    finished_at
    paused_at
    id
    progress
    progress_pages
    progress_seconds
    started_at
    user_book_id
    edition {
      title
      id
    }
  }
}
```

```json
{
  "asin": "1250791456"
}
```

## User Book by ASIN

```graphql
query UserBookByASIN($asin: String!) {
  user_books(where: {book:{editions: {asin: {_eq: $asin}, reading_format: {id: {_eq: 2}}}}}, limit: 1) {
    id
    book{
    title
    audio_seconds
    book_status_id
    canonical_id
    editions(where: {asin: {_eq: $asin}, reading_format: {id: {_eq: 2}}}, limit: 1) {
      id
      asin
      isbn_13: isbn_13
      isbn_10: isbn_10
      title
      edition_format
      reading_format {
        id
      }
      audio_seconds
      cached_image(path: "url")
      publisher {
        id
        name
      }
      language {
        id
        language
      }
      image {
        id
        url
      }
    }
    }
  }
}
```

```json
{
  "asin": "1250791456"
}
```

## Narrator by Name

```graphql
query NarratorByName($name: String!) {
  people(where: {name: {_eq: $name}}) {
    id
    name
  }
}
```

```json
{
  "name": "John Doe"
}
```
