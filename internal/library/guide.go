package library

// FormatGuide explains the music sources file to people and to AI assistants that edit it.
const FormatGuide = `Music sources configuration (JSON)

{"version": 1, "sources": [source, ...]}. Sources show in this order; the built-in file is a complete example.

A source:
  id        lowercase letters, digits and dashes; names the source in tools and caches (required)
  name      text shown on its chip (required)
  kind      "music" or "effects" (required)
  mode      "search" (default): every search is sent to the site
            "catalog": one complete list is downloaded and searched on this computer
  disabled  true hides the source
  notInAll  true leaves it out of the combined "All" search
  note      line shown under the results; hint: placeholder of the search box
  filters   catalog only: genre names offered as quick filters
  featured  catalog only: how many matches lead the "All" results (default 10)
  pageSize  results per page (default 20)
  request   {"url": template, "params": {name: template or [templates]}, "headers": {name: template},
             "cacheHours": number}. Parameters whose value renders empty are left out. Answers must be JSON;
             they are cached (search 24 h, catalog 168 h by default).
  results   path of the list of results in the answer ("" when the answer itself is the list)
  pages     path of the number of pages, or total: path of the number of results; with neither, the source is
            asked for {$fetch} = pageSize + 1 results and has another page when it returns more than pageSize
  error     path of an error message the site may return
  where     {path: pattern} keeps only results whose values match
  let       {name: {"from": path of a list, "where": {path: pattern}}} picks the first matching element,
            read later as {$let.name.field}
  expand    {"url", "params", "headers", "cacheHours", "results", "where", "limit"}: fetches a second document for
            every result (read as {$parent.field}; the whole document is {$doc.field}) and makes one track of each
            element of its results list
  fields    templates of the track: id, title and url are required; artist, genre, mood, keywords (extra search
            words), duration, durationUnit ("seconds" default, or "milliseconds"; "h:mm:ss" is always understood),
            referer (sent when downloading), ext (file type, default mp3), license, credit (written next to the
            export when not empty), page (web page of the track)
  rules     [{"if": template, "matches": pattern, "set": {field: template}}] override fields
  maps      {name: {key: value}} lookup tables for the map filter

Templates are text with {expressions}: one or more comma-separated paths (the first that is not empty is used)
followed by filters, as in {user_real_name,user_name|trim}. Write {{ and }} for literal braces.
Paths are dotted, numbers index lists: {files.0.name}. Request variables: {$query} {$page} {$offset} {$limit}
{$fetch}. Patterns: * matches any text, ignoring case; "*" alone means "not empty".

Filters (lists are handled element by element):
  path, query   escape for a URL path or query          quote   wrap in double quotes
  trim, lower, upper                                     ext     file extension of a URL
  map:NAME      look up in a map (missing keys give "")  strip:X / unsuffix:X  remove a prefix / suffix
  replace:A,B   replace text A with B                    default:X  X when the value is empty
  split:SEP     text into a list                         join:SEP   list into text, skipping empty items
  words         drop empty list items                    first      first non-empty item
  take:N        first N items                            drop:A,B   remove items equal to A or B
  ifset:X       X with %s replaced by the value, or "" when empty
  ifunset:X     X when the value is empty, otherwise ""

Only add sites whose terms allow downloading the files, and keep licences that allow how the videos will be used.`
